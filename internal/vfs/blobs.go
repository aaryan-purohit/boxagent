package vfs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const dirPerm = 0o700

// blobs stores content on disk under its SHA-256 hash.
type blobs struct{ dir string }

func hashOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func (b blobs) path(hash string) string {
	return filepath.Join(b.dir, hash[:2], hash)
}

// put stores content under hash, doing nothing if it is already stored.
func (b blobs) put(hash string, content []byte) error {
	dst := b.path(hash)
	_, err := os.Stat(dst)
	if err == nil {
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return err
	}
	return writeAtomic(dst, content)
}

func (b blobs) get(hash string) ([]byte, error) {
	content, err := os.ReadFile(b.path(hash))
	if err != nil {
		return nil, err
	}
	if got := hashOf(content); got != hash {
		return nil, fmt.Errorf("blob %s is corrupt: content hashes to %s", hash, got)
	}
	return content, nil
}

// remove deletes a blob, doing nothing if it is already gone.
func (b blobs) remove(hash string) error {
	if err := os.Remove(b.path(hash)); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// writeAtomic writes a temp file next to dst and renames it, so readers never
// see a partial file.
func writeAtomic(dst string, content []byte) (err error) {
	f, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = f.Close()           // may already be closed
			_ = os.Remove(f.Name()) // best effort; the first error matters
		}
	}()
	if _, err = f.Write(content); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), dst)
}
