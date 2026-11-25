package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"time"

	"github.com/moby/moby/client"
)

const maxFileBytes = 5 << 20

var (
	// ErrNotRegular means a path is a directory, symlink or other special file.
	ErrNotRegular = errors.New("not a regular file")
	// ErrTooLarge means a file exceeds the copy limit.
	ErrTooLarge = errors.New("file too large")
)

// CopyIn writes content to name, a validated path relative to /workspace,
// creating parent directories.
func (s *Sandbox) CopyIn(ctx context.Context, name string, content []byte) error {
	archive, err := fileArchive(name, content, time.Now())
	if err != nil {
		return fmt.Errorf("sandbox copy in %q: %w", name, err)
	}
	_, err = s.cli.CopyToContainer(ctx, s.id, client.CopyToContainerOptions{DestinationPath: workspace, Content: archive})
	if err != nil {
		return fmt.Errorf("sandbox copy in %q: %w", name, err)
	}
	return nil
}

// CopyOut returns the content of name, a validated path relative to
// /workspace.
func (s *Sandbox) CopyOut(ctx context.Context, name string) ([]byte, error) {
	res, err := s.cli.CopyFromContainer(ctx, s.id, client.CopyFromContainerOptions{SourcePath: path.Join(workspace, name)})
	if err != nil {
		return nil, fmt.Errorf("sandbox copy out %q: %w", name, err)
	}
	defer func() { _ = res.Content.Close() }() // read-only, error irrelevant
	if !res.Stat.Mode.IsRegular() {
		return nil, fmt.Errorf("sandbox copy out %q: %w", name, ErrNotRegular)
	}
	content, err := readFile(res.Content, maxFileBytes)
	if err != nil {
		return nil, fmt.Errorf("sandbox copy out %q: %w", name, err)
	}
	return content, nil
}

// readFile returns the content of the single regular file in archive. It
// checks type and size itself, because the file may change after a stat.
func readFile(archive io.Reader, limit int64) ([]byte, error) {
	tr := tar.NewReader(archive)
	hdr, err := tr.Next()
	if err != nil {
		return nil, err
	}
	if hdr.Typeflag != tar.TypeReg {
		return nil, ErrNotRegular
	}
	content, err := io.ReadAll(io.LimitReader(tr, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("%w: limit %d bytes", ErrTooLarge, limit)
	}
	return content, nil
}

// fileArchive returns a tar of one file and its parent directories.
func fileArchive(name string, content []byte, modTime time.Time) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for i, c := range name {
		if c != '/' {
			continue
		}
		if err := tw.WriteHeader(dirHeader(name[:i], modTime)); err != nil {
			return nil, err
		}
	}
	hdr := &tar.Header{
		Typeflag: tar.TypeReg, Name: name, Size: int64(len(content)),
		Mode: 0o644, Uid: uid, Gid: uid, ModTime: modTime,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return nil, err
	}
	if _, err := tw.Write(content); err != nil {
		return nil, err
	}
	return &buf, tw.Close()
}

func dirHeader(name string, modTime time.Time) *tar.Header {
	return &tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: 0o755, Uid: uid, Gid: uid, ModTime: modTime}
}
