// Package session runs one task in its own sandbox.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/aaryan-purohit/boxagent/internal/agent"
)

// destroyTimeout bounds cleanup, which runs even after cancellation.
const destroyTimeout = 15 * time.Second

// Sandbox is the part of a sandbox's lifecycle a session owns.
type Sandbox interface {
	CopyIn(ctx context.Context, path string, content []byte) error
	Destroy(ctx context.Context) error
}

// Runner works on a task.
type Runner interface {
	Run(ctx context.Context, task string) (agent.Result, error)
}

// SeedFile is a file to place in the sandbox before the agent starts.
type SeedFile struct {
	Path string
	Read func(context.Context) ([]byte, error)
}

// Options configures a session. S is the concrete sandbox type, so that
// NewAgent can give tools access to more than its lifecycle.
type Options[S Sandbox] struct {
	Task     string
	Seed     []SeedFile
	Create   func(context.Context) (S, error)
	NewAgent func(S) (Runner, error)
}

// NewID returns a unique session ID.
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // documented never to fail
	return hex.EncodeToString(b)
}

// Run creates a sandbox, seeds it, runs the agent and destroys the sandbox.
// Once created, the sandbox is destroyed however the run ends.
func Run[S Sandbox](ctx context.Context, opts Options[S]) (res agent.Result, err error) {
	sandbox, err := opts.Create(ctx)
	if err != nil {
		return agent.Result{}, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), destroyTimeout)
		defer cancel()
		err = errors.Join(err, sandbox.Destroy(cleanupCtx))
	}()
	if err := seed(ctx, sandbox, opts.Seed); err != nil {
		return agent.Result{}, err
	}
	runner, err := opts.NewAgent(sandbox)
	if err != nil {
		return agent.Result{}, err
	}
	return runner.Run(ctx, opts.Task)
}

func seed(ctx context.Context, sandbox Sandbox, files []SeedFile) error {
	for _, f := range files {
		content, err := f.Read(ctx)
		if err != nil {
			return fmt.Errorf("seed %q: %w", f.Path, err)
		}
		if err := sandbox.CopyIn(ctx, f.Path, content); err != nil {
			return fmt.Errorf("seed %q: %w", f.Path, err)
		}
	}
	return nil
}
