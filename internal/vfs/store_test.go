package vfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

const helloHash = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

// now has a zone and sub-second part, to prove timestamps are normalised.
var (
	now    = time.Date(2026, 1, 2, 5, 4, 5, 678, time.FixedZone("X", 2*60*60))
	stored = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

func openStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(t.Context(), dir, func() time.Time { return now })
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close() error: %v", err)
		}
	})
	return s
}

func mustPath(t *testing.T, raw string) Path {
	t.Helper()
	p, err := ParsePath(raw)
	if err != nil {
		t.Fatalf("ParsePath(%q) error: %v", raw, err)
	}
	return p
}

func mustWrite(t *testing.T, s *Store, raw, content string) Version {
	t.Helper()
	v, err := s.Write(t.Context(), mustPath(t, raw), []byte(content), "s1")
	if err != nil {
		t.Fatalf("Write(%q) error: %v", raw, err)
	}
	return v
}

// blobFiles returns every file under blobs/, including leftover temp files.
func blobFiles(t *testing.T, dir string) []string {
	t.Helper()
	root := filepath.Join(dir, "blobs")
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		files = append(files, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		t.Fatalf("walk blobs: %v", err)
	}
	return files
}

func TestWriteFirstVersion(t *testing.T) {
	t.Parallel()
	s := openStore(t, t.TempDir())

	got := mustWrite(t, s, "a.txt", "hello")

	want := Version{Path: "a.txt", Version: 1, Hash: helloHash, Size: 5, SessionID: "s1", CreatedAt: stored}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Write() mismatch (-want +got):\n%s", diff)
	}
}

func TestWriteChangedContentAddsVersion(t *testing.T) {
	t.Parallel()
	s := openStore(t, t.TempDir())
	mustWrite(t, s, "a.txt", "one")
	mustWrite(t, s, "a.txt", "two")

	got := mustWrite(t, s, "a.txt", "one") // same as version 1, but not as the latest

	if got.Version != 3 {
		t.Errorf("Write() version = %d, want 3", got.Version)
	}
}

func TestWriteIdenticalContentIsNoOp(t *testing.T) {
	t.Parallel()
	s := openStore(t, t.TempDir())
	first := mustWrite(t, s, "a.txt", "hello")

	second, err := s.Write(t.Context(), mustPath(t, "a.txt"), []byte("hello"), "s2")

	if err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	if diff := cmp.Diff(first, second); diff != "" {
		t.Errorf("Write() mismatch (-first +second):\n%s", diff)
	}
	history, err := s.History(t.Context(), mustPath(t, "a.txt"))
	if err != nil || len(history) != 1 {
		t.Errorf("History() = %v, %v, want one version", history, err)
	}
}

func TestWriteDedupesBlobsAcrossPaths(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := openStore(t, dir)

	mustWrite(t, s, "a.txt", "hello")
	mustWrite(t, s, "b.txt", "hello")

	want := []string{"2c/" + helloHash}
	if diff := cmp.Diff(want, blobFiles(t, dir)); diff != "" {
		t.Errorf("blobs mismatch (-want +got):\n%s", diff)
	}
}

func TestWriteSizeLimit(t *testing.T) {
	t.Parallel()
	s := openStore(t, t.TempDir())
	p := mustPath(t, "big")

	if _, err := s.Write(t.Context(), p, make([]byte, 5<<20+1), "s1"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Write() over the limit error = %v, want ErrTooLarge", err)
	}
	if _, _, err := s.Read(t.Context(), p, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("Read() after rejected write error = %v, want ErrNotFound", err)
	}
	if _, err := s.Write(t.Context(), p, make([]byte, 5<<20), "s1"); err != nil {
		t.Errorf("Write() at the limit error = %v, want nil", err)
	}
}

func TestWriteCancelledContext(t *testing.T) {
	t.Parallel()
	s := openStore(t, t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := s.Write(ctx, mustPath(t, "a"), []byte("x"), "s1"); !errors.Is(err, context.Canceled) {
		t.Errorf("Write() error = %v, want context.Canceled", err)
	}
}

func TestWriteConcurrentlyFromTwoStores(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stores := []*Store{openStore(t, dir), openStore(t, dir)} // like two processes
	p := mustPath(t, "a.txt")
	const writes = 20

	var wg sync.WaitGroup
	for i := range writes {
		wg.Go(func() {
			if _, err := stores[i%2].Write(t.Context(), p, fmt.Appendf(nil, "content %d", i), "s1"); err != nil {
				t.Errorf("Write() error: %v", err)
			}
		})
	}
	wg.Wait()

	history, err := stores[0].History(t.Context(), p)
	if err != nil {
		t.Fatalf("History() error: %v", err)
	}
	if len(history) != writes || history[0].Version != writes {
		t.Errorf("got %d versions, newest %d, want %d of each", len(history), history[0].Version, writes)
	}
}

func TestRead(t *testing.T) {
	t.Parallel()
	s := openStore(t, t.TempDir())
	mustWrite(t, s, "a.txt", "one")
	mustWrite(t, s, "a.txt", "two")
	mustWrite(t, s, "bin", "\x00\xff\xfe")
	mustWrite(t, s, "empty", "")
	tests := []struct {
		name, path  string
		version     int
		want        string
		wantVersion int
	}{
		{"reads the latest by default", "a.txt", 0, "two", 2},
		{"reads a specific version", "a.txt", 1, "one", 1},
		{"reads binary content unchanged", "bin", 0, "\x00\xff\xfe", 1},
		{"reads an empty file", "empty", 0, "", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			content, v, err := s.Read(t.Context(), mustPath(t, tc.path), tc.version)

			if err != nil {
				t.Fatalf("Read() error: %v", err)
			}
			if string(content) != tc.want || v.Version != tc.wantVersion {
				t.Errorf("Read() = %q version %d, want %q version %d", content, v.Version, tc.want, tc.wantVersion)
			}
		})
	}
}

func TestReadNotFound(t *testing.T) {
	t.Parallel()
	s := openStore(t, t.TempDir())
	mustWrite(t, s, "a.txt", "one")
	tests := []struct {
		name, path string
		version    int
	}{
		{"unknown path", "nope", 0},
		{"unknown version", "a.txt", 2},
		{"negative version", "a.txt", -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := s.Read(t.Context(), mustPath(t, tc.path), tc.version); !errors.Is(err, ErrNotFound) {
				t.Errorf("Read() error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestReadDetectsCorruptBlob(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := openStore(t, dir)
	mustWrite(t, s, "a.txt", "hello")
	if err := os.WriteFile(filepath.Join(dir, "blobs", "2c", helloHash), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}

	if content, _, err := s.Read(t.Context(), mustPath(t, "a.txt"), 0); err == nil {
		t.Errorf("Read() = %q, want an error", content)
	}
}

func TestList(t *testing.T) {
	t.Parallel()
	s := openStore(t, t.TempDir())
	for _, p := range []string{"src/b.go", "src/a.go", "src2/c.go", "a_b/x", "aXb/x", "é/f"} {
		mustWrite(t, s, p, "old")
	}
	mustWrite(t, s, "src/a.go", "newer")
	tests := []struct {
		name, prefix string
		want         []string
	}{
		{"empty prefix lists everything", "", []string{"aXb/x@1", "a_b/x@1", "src/a.go@2", "src/b.go@1", "src2/c.go@1", "é/f@1"}},
		{"directory matches on segment boundary", "src", []string{"src/a.go@2", "src/b.go@1"}},
		{"trailing slash is ignored", "src2/", []string{"src2/c.go@1"}},
		{"exact file", "src/b.go", []string{"src/b.go@1"}},
		{"underscore is literal", "a_b", []string{"a_b/x@1"}},
		{"percent is literal", "%", nil},
		{"unicode directory", "é", []string{"é/f@1"}},
		{"no match", "sr", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prefix, err := ParsePrefix(tc.prefix)
			if err != nil {
				t.Fatalf("ParsePrefix() error: %v", err)
			}

			vs, err := s.List(t.Context(), prefix)

			if err != nil {
				t.Fatalf("List() error: %v", err)
			}
			var got []string
			for _, v := range vs {
				got = append(got, fmt.Sprintf("%s@%d", v.Path, v.Version))
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("List() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestListDescribesLatestVersion(t *testing.T) {
	t.Parallel()
	s := openStore(t, t.TempDir())
	mustWrite(t, s, "a.txt", "a much longer first version")
	mustWrite(t, s, "a.txt", "hello")

	got, err := s.List(t.Context(), Prefix{})

	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	want := []Version{{Path: "a.txt", Version: 2, Hash: helloHash, Size: 5, SessionID: "s1", CreatedAt: stored}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("List() mismatch (-want +got):\n%s", diff)
	}
}

func TestHistoryNewestFirst(t *testing.T) {
	t.Parallel()
	s := openStore(t, t.TempDir())
	v1 := mustWrite(t, s, "a.txt", "one")
	v2 := mustWrite(t, s, "a.txt", "two")
	mustWrite(t, s, "other", "x")

	got, err := s.History(t.Context(), mustPath(t, "a.txt"))

	if err != nil {
		t.Fatalf("History() error: %v", err)
	}
	if diff := cmp.Diff([]Version{v2, v1}, got); diff != "" {
		t.Errorf("History() mismatch (-want +got):\n%s", diff)
	}
}

func TestHistoryNotFound(t *testing.T) {
	t.Parallel()
	s := openStore(t, t.TempDir())

	if _, err := s.History(t.Context(), mustPath(t, "nope")); !errors.Is(err, ErrNotFound) {
		t.Errorf("History() error = %v, want ErrNotFound", err)
	}
}

func TestOpenKeepsDataAndHandlesOddDirNames(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "odd ?#% dir", "nested")
	first, err := Open(t.Context(), dir, time.Now)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	mustWrite(t, first, "a.txt", "hello")
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	content, _, err := openStore(t, dir).Read(t.Context(), mustPath(t, "a.txt"), 0)

	if err != nil || string(content) != "hello" {
		t.Errorf("Read() after reopen = %q, %v, want hello", content, err)
	}
	info, err := os.Stat(filepath.Join(dir, "boxagent.db"))
	if err != nil {
		t.Fatalf("database is not in the data dir: %v", err)
	}
	if info.IsDir() {
		t.Error("boxagent.db is a directory")
	}
	if info, err = os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("data dir mode = %v, %v, want 0700", info.Mode().Perm(), err)
	}
}

func TestWriteRejectsPathConflicts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, existing, path string
		want                 error
	}{
		{"file where a directory exists", "src/a", "src", ErrPathConflict},
		{"file where a deep directory exists", "a/b/c", "a/b", ErrPathConflict},
		{"file inside a file", "src", "src/a", ErrPathConflict},
		{"file deep inside a file", "a", "a/b/c", ErrPathConflict},
		{"new version of the same file", "src/a", "src/a", nil},
		{"sibling", "src/a", "src/b", nil},
		{"name that shares a prefix", "src/a", "src2", nil},
		{"name that another name starts with", "src2", "src", nil},
		{"wildcard characters", "a_b/x", "a%", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			s := openStore(t, dir)
			mustWrite(t, s, tc.existing, "one")

			_, err := s.Write(t.Context(), mustPath(t, tc.path), []byte("two"), "s1")

			if !errors.Is(err, tc.want) {
				t.Errorf("Write() error = %v, want %v", err, tc.want)
			}
			if tc.want != nil && len(blobFiles(t, dir)) != 1 {
				t.Errorf("rejected write left a blob: %v", blobFiles(t, dir))
			}
		})
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := openStore(t, dir)
	mustWrite(t, s, "src/a", "one")
	mustWrite(t, s, "src/a", "two")
	mustWrite(t, s, "src/b", "hello")
	mustWrite(t, s, "src2/c", "hello") // shares a blob with src/b
	prefix, err := ParsePrefix("src")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Delete(t.Context(), prefix); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	left, err := s.List(t.Context(), Prefix{})
	if err != nil || len(left) != 1 || left[0].Path != "src2/c" {
		t.Errorf("List() = %+v, %v, want only src2/c", left, err)
	}
	if diff := cmp.Diff([]string{"2c/" + helloHash}, blobFiles(t, dir)); diff != "" {
		t.Errorf("blobs mismatch (-want +got):\n%s", diff)
	}
	if content, _, err := s.Read(t.Context(), mustPath(t, "src2/c"), 0); err != nil || string(content) != "hello" {
		t.Errorf("Read() of the kept file = %q, %v, want hello", content, err)
	}
	if v := mustWrite(t, s, "src/a", "two"); v.Version != 1 {
		t.Errorf("version after delete = %d, want 1", v.Version)
	}
}

func TestDeleteEverything(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := openStore(t, dir)
	mustWrite(t, s, "a", "1")
	mustWrite(t, s, "b/c", "2")

	if err := s.Delete(t.Context(), Prefix{}); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	if left := blobFiles(t, dir); len(left) != 0 {
		t.Errorf("blobs left: %v", left)
	}
}

func TestDeleteNotFound(t *testing.T) {
	t.Parallel()
	s := openStore(t, t.TempDir())

	if err := s.Delete(t.Context(), Prefix{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
}

func TestDeleteAndWriteConcurrently(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writer, deleter := openStore(t, dir), openStore(t, dir)
	kept, other := mustPath(t, "kept"), Prefix{"other"}

	var wg sync.WaitGroup
	wg.Go(func() { // each delete could remove the blob that the other goroutine reuses
		for range 20 {
			if _, err := deleter.Write(t.Context(), Path(other), []byte("shared"), "s1"); err != nil {
				t.Errorf("Write(other) error: %v", err)
			}
			if err := deleter.Delete(t.Context(), other); err != nil {
				t.Errorf("Delete() error: %v", err)
			}
		}
	})
	wg.Go(func() {
		for i := range 20 {
			for _, content := range []string{"shared", fmt.Sprint(i)} {
				if _, err := writer.Write(t.Context(), kept, []byte(content), "s1"); err != nil {
					t.Errorf("Write(kept) error: %v", err)
				}
			}
		}
	})
	wg.Wait()

	for version := 1; version <= 40; version++ {
		if _, _, err := writer.Read(t.Context(), kept, version); err != nil {
			t.Errorf("Read(version %d) error: %v", version, err)
		}
	}
}
