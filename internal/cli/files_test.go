package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/aaryan-purohit/boxagent/internal/sandbox/docker"
	"github.com/aaryan-purohit/boxagent/internal/tools"
	"github.com/aaryan-purohit/boxagent/internal/vfs"
)

func TestSessionFiles(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	store, err := vfs.Open(t.Context(), t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() }) // best effort
	f := sessionFiles{store, "s1"}
	want := tools.FileVersion{Path: "a/b", Version: 1, Hash: oneHash, Size: 3, CreatedAt: now}

	got, err := f.Write(t.Context(), "./a//b", []byte("one"))
	if diff := cmp.Diff(want, got); diff != "" || err != nil {
		t.Errorf("Write() error %v, mismatch (-want +got):\n%s", err, diff)
	}
	content, got, err := f.Read(t.Context(), "a/b", 0)
	if diff := cmp.Diff(want, got); diff != "" || err != nil || string(content) != "one" {
		t.Errorf("Read() = %q, error %v, mismatch (-want +got):\n%s", content, err, diff)
	}
	list, err := f.List(t.Context(), "a")
	if diff := cmp.Diff([]tools.FileVersion{want}, list); diff != "" || err != nil {
		t.Errorf("List() error %v, mismatch (-want +got):\n%s", err, diff)
	}
	history, err := f.History(t.Context(), "a/b")
	if diff := cmp.Diff([]tools.FileVersion{want}, history); diff != "" || err != nil {
		t.Errorf("History() error %v, mismatch (-want +got):\n%s", err, diff)
	}
	p, err := vfs.ParsePath("a/b")
	if err != nil {
		t.Fatal(err)
	}
	if vs, err := store.History(t.Context(), p); err != nil || vs[0].SessionID != "s1" {
		t.Errorf("stored versions = %+v, %v, want session s1", vs, err)
	}
}

type fakeContainer struct{ copied []string }

func (*fakeContainer) Exec(_ context.Context, cmd string, _ time.Duration) (docker.ExecResult, error) {
	return docker.ExecResult{Stdout: cmd, Stderr: "e", ExitCode: 1, TimedOut: true}, nil
}

func (c *fakeContainer) CopyOut(_ context.Context, path string) ([]byte, error) {
	c.copied = append(c.copied, path)
	return nil, nil
}

func TestSandboxTools(t *testing.T) {
	t.Parallel()
	c := &fakeContainer{}
	s := sandboxTools{c}

	got, err := s.Exec(t.Context(), "ls", time.Second)

	want := tools.ExecResult{Stdout: "ls", Stderr: "e", ExitCode: 1, TimedOut: true}
	if diff := cmp.Diff(want, got); diff != "" || err != nil {
		t.Errorf("Exec() error %v, mismatch (-want +got):\n%s", err, diff)
	}
	if _, err := s.CopyOut(t.Context(), "./a"); err != nil {
		t.Errorf("CopyOut() error: %v", err)
	}
	if diff := cmp.Diff([]string{"a"}, c.copied); diff != "" {
		t.Errorf("copied mismatch (-want +got):\n%s", diff)
	}
}

func TestAdaptersRejectBadPaths(t *testing.T) {
	t.Parallel()
	const bad = "../x"
	ctx := t.Context()
	f, c := sessionFiles{}, &fakeContainer{}
	calls := map[string]func() error{
		"Write":   func() error { _, err := f.Write(ctx, bad, nil); return err },
		"Read":    func() error { _, _, err := f.Read(ctx, bad, 0); return err },
		"List":    func() error { _, err := f.List(ctx, bad); return err },
		"History": func() error { _, err := f.History(ctx, bad); return err },
		"CopyOut": func() error { _, err := sandboxTools{c}.CopyOut(ctx, bad); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, vfs.ErrInvalidPath) {
			t.Errorf("%s(%q) error = %v, want ErrInvalidPath", name, bad, err)
		}
	}
	if len(c.copied) != 0 {
		t.Errorf("container was asked for %q", c.copied)
	}
}
