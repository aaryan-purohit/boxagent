package vfs

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

const (
	dbName       = "boxagent.db"
	blobDir      = "blobs"
	maxFileBytes = 5 << 20
	latest       = 0 // version number that selects the newest version
	// underPrefix matches paths under the prefix ?1. substr, unlike LIKE, treats
	// % and _ literally.
	underPrefix = "(?1 = '' OR path = ?1 OR substr(path, 1, length(?1) + 1) = ?1 || '/')"
	columns     = "path, version, hash, size, session_id, created_at"
	// Wait for other BoxAgent processes, and take the write lock when a
	// transaction begins so read-then-insert can't deadlock.
	dsnParams = "_pragma=busy_timeout(10000)&_txlock=immediate"
)

//go:embed schema.sql
var schema string

var (
	// ErrNotFound means the path or version does not exist.
	ErrNotFound = errors.New("not found")
	// ErrTooLarge means the content exceeds the single-file limit.
	ErrTooLarge = errors.New("file too large")
	// ErrPathConflict means a path would be both a file and a directory.
	ErrPathConflict = errors.New("path conflicts with an existing file")
)

// Version describes one stored version of a file.
type Version struct {
	Path      string
	Version   int
	Hash      string
	Size      int64
	SessionID string
	CreatedAt time.Time
}

// Store keeps every version of every file.
type Store struct {
	db    *sql.DB
	blobs blobs
	clock func() time.Time
}

// Open opens the store in dataDir, creating it if needed. Close it after use.
func Open(ctx context.Context, dataDir string, clock func() time.Time) (*Store, error) {
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("vfs open %q: %w", dataDir, err)
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("vfs open: %w", err)
	}
	dsn := url.URL{Scheme: "file", Path: filepath.Join(dir, dbName), RawQuery: dsnParams}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, fmt.Errorf("vfs open %q: %w", dir, err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, errors.Join(fmt.Errorf("vfs open %q: apply schema: %w", dir, err), db.Close())
	}
	return &Store{db: db, blobs: blobs{filepath.Join(dir, blobDir)}, clock: clock}, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// Write stores content as the next version of p. Content identical to the
// latest version is a no-op that returns that version.
func (s *Store) Write(ctx context.Context, p Path, content []byte, sessionID string) (Version, error) {
	if len(content) > maxFileBytes {
		return Version{}, fmt.Errorf("vfs write %q: %w: %d bytes, limit %d", p, ErrTooLarge, len(content), maxFileBytes)
	}
	v := Version{
		Path:      p.String(),
		Hash:      hashOf(content),
		Size:      int64(len(content)),
		SessionID: sessionID,
		CreatedAt: s.clock().UTC().Truncate(time.Second),
	}
	v, err := s.record(ctx, p, v, content)
	if err != nil {
		return Version{}, fmt.Errorf("vfs write %q: %w", p, err)
	}
	return v, nil
}

// record assigns v its version number, then stores its content and row, in one
// transaction. Blobs change only under the transaction's write lock, so Delete
// can't remove one that is about to be used. The blob goes first: a crash
// before the commit leaves only an unused blob.
func (s *Store) record(ctx context.Context, p Path, v Version, content []byte) (Version, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Version{}, err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit

	prev, err := find(ctx, tx, p, latest)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Version{}, err
	}
	if prev.Hash == v.Hash {
		return prev, nil
	}
	if prev.Version == 0 {
		if err := checkConflict(ctx, tx, p); err != nil {
			return Version{}, err
		}
	}
	if err := s.blobs.put(v.Hash, content); err != nil {
		return Version{}, err
	}
	v.Version = prev.Version + 1
	_, err = tx.ExecContext(ctx, "INSERT INTO versions ("+columns+") VALUES (?, ?, ?, ?, ?, ?)",
		v.Path, v.Version, v.Hash, v.Size, v.SessionID, v.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return Version{}, err
	}
	return v, tx.Commit()
}

// checkConflict fails if p is a directory of an existing file, or one of p's
// directories is an existing file.
func checkConflict(ctx context.Context, q rowQuerier, p Path) error {
	var other string
	err := q.QueryRowContext(ctx, `
		SELECT path FROM versions
		WHERE substr(path, 1, length(?1) + 1) = ?1 || '/'
		   OR substr(?1, 1, length(path) + 1) = path || '/'
		LIMIT 1`, p.String()).Scan(&other)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: %q", ErrPathConflict, other)
}

// Delete permanently removes every version of every file under prefix, and any
// content no other file uses.
func (s *Store) Delete(ctx context.Context, prefix Prefix) error {
	hashes, err := s.deleteRows(ctx, prefix)
	if err != nil {
		return fmt.Errorf("vfs delete %q: %w", prefix.s, err)
	}
	if err := s.removeUnused(ctx, hashes); err != nil {
		return fmt.Errorf("vfs delete %q: %w", prefix.s, err)
	}
	return nil
}

// deleteRows deletes the rows under prefix and returns the hashes they used.
func (s *Store) deleteRows(ctx context.Context, prefix Prefix) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "DELETE FROM versions WHERE "+underPrefix+" RETURNING hash", prefix.s)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // rows.Err reports iteration errors

	used := map[string]bool{}
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		used[hash] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(used) == 0 {
		return nil, ErrNotFound
	}
	return slices.Sorted(maps.Keys(used)), nil
}

// removeUnused removes the blobs among hashes that no row refers to. It writes
// no rows; the transaction only holds the write lock.
func (s *Store) removeUnused(ctx context.Context, hashes []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // nothing to commit

	for _, hash := range hashes {
		var inUse bool
		err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM versions WHERE hash = ?)", hash).Scan(&inUse)
		if err != nil {
			return err
		}
		if inUse {
			continue
		}
		if err := s.blobs.remove(hash); err != nil {
			return err
		}
	}
	return nil
}

// Read returns the content of p at version, or at the latest version if
// version is 0.
func (s *Store) Read(ctx context.Context, p Path, version int) ([]byte, Version, error) {
	v, err := find(ctx, s.db, p, version)
	if err != nil {
		return nil, Version{}, fmt.Errorf("vfs read %q version %d: %w", p, version, err)
	}
	content, err := s.blobs.get(v.Hash)
	if err != nil {
		return nil, Version{}, fmt.Errorf("vfs read %q version %d: %w", p, v.Version, err)
	}
	return content, v, nil
}

// List returns the latest version of every file under prefix, sorted by path.
func (s *Store) List(ctx context.Context, prefix Prefix) ([]Version, error) {
	// SQLite takes the bare columns from the row that holds MAX(version).
	vs, err := s.query(ctx, `
		SELECT path, MAX(version), hash, size, session_id, created_at FROM versions
		WHERE `+underPrefix+` GROUP BY path ORDER BY path`, prefix.s)
	if err != nil {
		return nil, fmt.Errorf("vfs list %q: %w", prefix.s, err)
	}
	return vs, nil
}

// History returns every version of p, newest first.
func (s *Store) History(ctx context.Context, p Path) ([]Version, error) {
	vs, err := s.query(ctx, "SELECT "+columns+" FROM versions WHERE path = ? ORDER BY version DESC", p.String())
	if err == nil && len(vs) == 0 {
		err = ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("vfs history %q: %w", p, err)
	}
	return vs, nil
}

func (s *Store) query(ctx context.Context, q string, args ...any) ([]Version, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // rows.Err reports iteration errors

	var vs []Version
	for rows.Next() {
		v, err := scan(rows.Scan)
		if err != nil {
			return nil, err
		}
		vs = append(vs, v)
	}
	return vs, rows.Err()
}

// rowQuerier is satisfied by both *sql.DB and *sql.Tx.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func find(ctx context.Context, q rowQuerier, p Path, version int) (Version, error) {
	row := q.QueryRowContext(ctx, "SELECT "+columns+` FROM versions
		WHERE path = ?1 AND (?2 = 0 OR version = ?2)
		ORDER BY version DESC LIMIT 1`, p.String(), version)
	v, err := scan(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	return v, err
}

func scan(scanRow func(...any) error) (Version, error) {
	var v Version
	var created string
	if err := scanRow(&v.Path, &v.Version, &v.Hash, &v.Size, &v.SessionID, &created); err != nil {
		return Version{}, err
	}
	var err error
	if v.CreatedAt, err = time.Parse(time.RFC3339, created); err != nil {
		return Version{}, err
	}
	return v, nil
}
