package cli

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/aaryan-purohit/boxagent/internal/agent"
	"github.com/aaryan-purohit/boxagent/internal/config"
	"github.com/aaryan-purohit/boxagent/internal/vfs"
)

// errReachedModel marks that every check before the model passed. No sandbox
// exists at that point, so these tests need no Docker.
var errReachedModel = errors.New("reached model creation")

func TestRunChecksBeforeSandbox(t *testing.T) {
	t.Parallel()
	seeded := seededEnv(t)
	withKey := func(key string) string {
		if key == "GEMINI_API_KEY" {
			return "k"
		}
		return seeded(key)
	}
	tests := []struct {
		name string
		env  func(string) string
		args []string
		want error
	}{
		{"accepts a task", withKey, []string{"task"}, errReachedModel},
		{"accepts all flags", withKey, []string{"task", "--seed", "src", "--max-turns", "1", "--network"}, errReachedModel},
		{"needs an API key", seeded, []string{"task"}, config.ErrMissingAPIKey},
		{"checks the key before the task", seeded, []string{""}, config.ErrMissingAPIKey},
		{"rejects a blank task", withKey, []string{" "}, errEmptyTask},
		{"rejects zero turns", withKey, []string{"task", "--max-turns", "0"}, agent.ErrInvalidMaxTurns},
		{"rejects an invalid seed", withKey, []string{"task", "--seed", "../x"}, vfs.ErrInvalidPath},
		{"accepts seeding everything", withKey, []string{"task", "--seed", "."}, errReachedModel},
		{"rejects an empty seed", withKey, []string{"task", "--seed", ""}, vfs.ErrInvalidPath},
		{"explains a task taken as the seed", withKey, []string{"--seed", "my task"}, errNoTask},
		{"rejects a seed with no files", withKey, []string{"task", "--seed", "nope"}, errEmptySeed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := newRunCmd(tc.env, func(context.Context, config.Config, *slog.Logger) (agent.Model, error) {
				return nil, errReachedModel
			})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(tc.args)

			if err := cmd.ExecuteContext(t.Context()); !errors.Is(err, tc.want) {
				t.Errorf("run error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRunArgCounts(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"run"}, {"run", "a", "b"}} {
		_, err := runCLI(t, noEnv, args...)
		if err == nil || errors.Is(err, config.ErrInvalid) {
			t.Errorf("Execute(%q) error = %v, want an argument error", args, err)
		}
	}
}

func TestSeedFilesReadsLatestVersions(t *testing.T) {
	t.Parallel()
	all := map[string]string{"src/a.txt": "two", "bad\x1b[2J": "evil"}
	for seed, want := range map[string]map[string]string{"src": {"src/a.txt": "two"}, ".": all} {
		t.Run(seed, func(t *testing.T) {
			t.Parallel()
			checkSeed(t, seed, want)
		})
	}
}

func checkSeed(t *testing.T, seed string, want map[string]string) {
	t.Helper()
	store, err := vfs.Open(t.Context(), seededEnv(t)("BOXAGENT_DATA_DIR"), nil) // only reads, so no clock
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() }) // best effort

	files, err := seedFiles(t.Context(), store, runOptions{seed: seed, hasSeed: true})

	if err != nil {
		t.Fatalf("seedFiles() error: %v", err)
	}
	got := map[string]string{}
	for _, f := range files {
		content, err := f.Read(t.Context())
		if err != nil {
			t.Fatalf("Read() error: %v", err)
		}
		got[f.Path] = string(content)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("seed mismatch (-want +got):\n%s", diff)
	}
}
