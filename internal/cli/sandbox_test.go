package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/moby/moby/client"
)

func TestSandboxPrune(t *testing.T) {
	t.Parallel()
	errFake := errors.New("fake")
	tests := []struct {
		name    string
		removed []string
		err     error
		want    string
	}{
		{"prints nothing when nothing is removed", nil, nil, ""},
		{"prints removed IDs", []string{"a", "b"}, nil, "a\nb\n"},
		{"prints removed IDs on partial failure", []string{"a"}, errFake, "a\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			cmd := newSandboxCmd(func(context.Context) ([]string, error) { return tc.removed, tc.err })
			cmd.SilenceUsage = true // the root command sets this
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"prune"})

			err := cmd.ExecuteContext(t.Context())

			if !errors.Is(err, tc.err) || out.String() != tc.want {
				t.Errorf("prune = %q, %v, want %q, %v", out.String(), err, tc.want, tc.err)
			}
		})
	}
}

func TestUnreachableDockerIsExplained(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/docker.sock")

	_, err := pruneDocker(t.Context())

	if !client.IsErrConnectionFailed(err) || !strings.Contains(err.Error(), "set DOCKER_HOST") {
		t.Errorf("pruneDocker() error = %v, want a connection error with advice", err)
	}
}

func TestExplainDockerKeepsOtherErrors(t *testing.T) {
	t.Parallel()
	other := errors.New("other")
	if got := explainDocker(other); got != other { //nolint:errorlint // must be unchanged
		t.Errorf("explainDocker() = %v, want it unchanged", got)
	}
	if got := explainDocker(nil); got != nil {
		t.Errorf("explainDocker(nil) = %v, want nil", got)
	}
}

func TestSandboxPruneRejectsArgs(t *testing.T) {
	t.Parallel()
	cmd := newSandboxCmd(func(context.Context) ([]string, error) {
		t.Error("prune ran despite bad arguments")
		return nil, nil
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"prune", "x"})

	if err := cmd.ExecuteContext(t.Context()); err == nil {
		t.Error("Execute() = nil, want an error")
	}
}
