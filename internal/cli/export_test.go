package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/aaryan-purohit/boxagent/internal/vfs"
)

// tree returns the files under dir and their content.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	got := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := os.ReadFile(p)
		rel, relErr := filepath.Rel(dir, p)
		got[filepath.ToSlash(rel)] = string(content)
		return errors.Join(err, relErr)
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return got
}

func TestVFSExport(t *testing.T) {
	t.Parallel()
	env := seededEnv(t)
	tests := []struct {
		name string
		args []string
		want map[string]string
	}{
		{"everything", nil, map[string]string{"src/a.txt": "two", "bad\x1b[2J": "evil"}},
		{"one directory", []string{"src"}, map[string]string{"src/a.txt": "two"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), "new", "dir")

			_, err := runCLI(t, env, append([]string{"vfs", "export", dir}, tc.args...)...)

			if err != nil {
				t.Fatalf("Execute() error: %v", err)
			}
			if diff := cmp.Diff(tc.want, tree(t, dir)); diff != "" {
				t.Errorf("exported files mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestVFSExportNeverOverwrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mine"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := runCLI(t, seededEnv(t), "vfs", "export", dir)

	if !errors.Is(err, errNotEmpty) {
		t.Errorf("Execute() error = %v, want errNotEmpty", err)
	}
	if diff := cmp.Diff(map[string]string{"mine": "keep"}, tree(t, dir)); diff != "" {
		t.Errorf("directory changed (-want +got):\n%s", diff)
	}
}

func TestVFSExportErrors(t *testing.T) {
	t.Parallel()
	env := seededEnv(t)
	for prefix, want := range map[string]error{"nope": errNoFiles, "../x": vfs.ErrInvalidPath, "": vfs.ErrInvalidPath} {
		dir := filepath.Join(t.TempDir(), "out")
		if _, err := runCLI(t, env, "vfs", "export", dir, prefix); !errors.Is(err, want) {
			t.Errorf("export %q error = %v, want %v", prefix, err, want)
		}
		if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("failed export %q created its directory", prefix)
		}
	}
}

func TestVFSRemoveNeedsYes(t *testing.T) {
	t.Parallel()
	env := seededEnv(t)

	out, err := runCLI(t, env, "vfs", "rm", "src")

	if !errors.Is(err, errNeedYes) || !strings.Contains(out, "src/a.txt") {
		t.Errorf("rm = %q, %v, want a listing and errNeedYes", out, err)
	}
	if _, err := runCLI(t, env, "vfs", "cat", "src/a.txt"); err != nil {
		t.Errorf("file was deleted without --yes: %v", err)
	}
}

func TestVFSRemove(t *testing.T) {
	t.Parallel()
	env := seededEnv(t)

	if _, err := runCLI(t, env, "vfs", "rm", "src", "--yes"); err != nil {
		t.Fatalf("rm error: %v", err)
	}

	if _, err := runCLI(t, env, "vfs", "history", "src/a.txt"); !errors.Is(err, vfs.ErrNotFound) {
		t.Errorf("history after rm error = %v, want ErrNotFound", err)
	}
	if out, err := runCLI(t, env, "vfs", "ls"); err != nil || !strings.Contains(out, "bad") {
		t.Errorf("ls after rm = %q, %v, want the other file kept", out, err)
	}
	if _, err := runCLI(t, env, "vfs", "rm", ".", "--yes"); err != nil {
		t.Errorf("rm . error: %v", err)
	}
	if out, err := runCLI(t, env, "vfs", "ls"); err != nil || out != "PATH  VERSION  SIZE\n" {
		t.Errorf("ls after rm . = %q, %v, want no files", out, err)
	}
}

func TestVFSRemoveErrors(t *testing.T) {
	t.Parallel()
	env := seededEnv(t)
	for prefix, want := range map[string]error{"nope": errNoFiles, "../x": vfs.ErrInvalidPath, "": vfs.ErrInvalidPath} {
		if _, err := runCLI(t, env, "vfs", "rm", prefix, "--yes"); !errors.Is(err, want) {
			t.Errorf("rm %q error = %v, want %v", prefix, err, want)
		}
	}
	if _, err := runCLI(t, env, "vfs", "rm", "--yes"); err == nil {
		t.Error("rm without a prefix = nil, want an error")
	}
}

func TestIsTerminal(t *testing.T) {
	t.Parallel()
	f, err := os.Create(filepath.Join(t.TempDir(), "f"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() }) // best effort
	if isTerminal(f) || isTerminal(&strings.Builder{}) {
		t.Error("isTerminal() = true for a file or buffer, want false")
	}
	null, err := os.Open(os.DevNull) // a character device, like a terminal
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = null.Close() }) // best effort
	if !isTerminal(null) {
		t.Error("isTerminal() = false for a character device, want true")
	}
}
