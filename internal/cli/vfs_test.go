package cli

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/aaryan-purohit/boxagent/internal/config"
	"github.com/aaryan-purohit/boxagent/internal/vfs"
)

const (
	oneHash = "7692c3ad3540bb803c020b3aee66cd8887123234ea0c6e7143c0add73ff431ed"
	twoHash = "3fc4ccfe745870e2c0d99f71f30ff0656c8dedd41cc1d7d3d376b0dbe685e2f3"
)

// seededEnv returns an environment whose data dir holds a few files.
func seededEnv(t *testing.T) func(string) string {
	t.Helper()
	dir := t.TempDir()
	clock := func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	s, err := vfs.Open(t.Context(), dir, clock)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	for _, f := range []struct{ path, content string }{
		{"src/a.txt", "one"},
		{"src/a.txt", "two"},
		{"bad\x1b[2J", "evil"},
	} {
		p, err := vfs.ParsePath(f.path)
		if err != nil {
			t.Fatalf("ParsePath() error: %v", err)
		}
		if _, err := s.Write(t.Context(), p, []byte(f.content), "s1"); err != nil {
			t.Fatalf("Write() error: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	return func(key string) string {
		if key == "BOXAGENT_DATA_DIR" {
			return dir
		}
		return ""
	}
}

func runCLI(t *testing.T, getenv func(string) string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := newRootCmd("dev", getenv)
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(t.Context())
	return out.String(), err
}

func TestVFSCommands(t *testing.T) {
	t.Parallel()
	env := seededEnv(t)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			"ls lists everything and quotes control characters",
			[]string{"vfs", "ls"},
			"PATH          VERSION  SIZE\n" +
				`"bad\x1b[2J"  1        4` + "\n" +
				"src/a.txt     2        3\n",
		},
		{
			"ls filters by prefix",
			[]string{"vfs", "ls", "src"},
			"PATH       VERSION  SIZE\nsrc/a.txt  2        3\n",
		},
		{"cat prints the latest version", []string{"vfs", "cat", "src/a.txt"}, "two"},
		{"cat prints a chosen version", []string{"vfs", "cat", "src/a.txt", "--version", "1"}, "one"},
		{
			"history prints newest first",
			[]string{"vfs", "history", "src/a.txt"},
			"VERSION  HASH                                                              SIZE  CREATED               SESSION\n" +
				"2        " + twoHash + "  3     2026-01-02T03:04:05Z  s1\n" +
				"1        " + oneHash + "  3     2026-01-02T03:04:05Z  s1\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := runCLI(t, env, tc.args...)

			if err != nil {
				t.Fatalf("Execute() error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("output mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestVFSCommandErrors(t *testing.T) {
	t.Parallel()
	env := seededEnv(t)
	tests := []struct {
		name string
		args []string
		want error
	}{
		{"ls rejects traversal", []string{"vfs", "ls", "../x"}, vfs.ErrInvalidPath},
		{"cat rejects an absolute path", []string{"vfs", "cat", "/etc/passwd"}, vfs.ErrInvalidPath},
		{"cat rejects version zero", []string{"vfs", "cat", "src/a.txt", "--version", "0"}, errBadVersion},
		{"cat reports a missing version", []string{"vfs", "cat", "src/a.txt", "--version", "9"}, vfs.ErrNotFound},
		{"cat reports a missing file", []string{"vfs", "cat", "nope"}, vfs.ErrNotFound},
		{"history rejects traversal", []string{"vfs", "history", "a/../b"}, vfs.ErrInvalidPath},
		{"history reports a missing file", []string{"vfs", "history", "nope"}, vfs.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := runCLI(t, env, tc.args...); !errors.Is(err, tc.want) {
				t.Errorf("Execute() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestVFSCommandArgCounts(t *testing.T) {
	t.Parallel()
	env := seededEnv(t)
	for _, args := range [][]string{
		{"vfs", "ls", "a", "b"},
		{"vfs", "cat"},
		{"vfs", "cat", "a", "b"},
		{"vfs", "history"},
		{"vfs", "nope"},
	} {
		if _, err := runCLI(t, env, args...); err == nil {
			t.Errorf("Execute(%q) = nil, want an error", args)
		}
	}
}

func TestVFSReportsBadConfig(t *testing.T) {
	t.Parallel()

	_, err := runCLI(t, noEnv, "vfs", "ls") // no HOME, no data dir

	if !errors.Is(err, config.ErrInvalid) {
		t.Errorf("Execute() error = %v, want config.ErrInvalid", err)
	}
}
