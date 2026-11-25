package docker

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

const (
	// execGrace is how long past a command's timeout the host waits for the
	// in-container kill to take effect.
	execGrace = 5 * time.Second
	// maxStreamBytes caps the host memory one output stream can use.
	maxStreamBytes = 1 << 20
	killedExitCode = 137 // 128 + SIGKILL
)

// ExecResult is the outcome of a command that ran.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
}

// Exec runs cmd with sh -c in /workspace, killing it and its process group
// after timeout (rounded up to whole seconds). Cancelling ctx abandons the
// command; it dies with the container.
func (s *Sandbox) Exec(ctx context.Context, cmd string, timeout time.Duration) (ExecResult, error) {
	created, err := s.cli.ExecCreate(ctx, s.id, client.ExecCreateOptions{
		Cmd:          withTimeout(cmd, timeout),
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return ExecResult{}, fmt.Errorf("sandbox exec: %w", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout+execGrace)
	defer cancel()
	began := time.Now()
	var stdout, stderr = capture{limit: maxStreamBytes}, capture{limit: maxStreamBytes}
	if err := s.stream(runCtx, created.ID, &stdout, &stderr); err != nil {
		return ExecResult{}, fmt.Errorf("sandbox exec: %w", err)
	}
	elapsed := time.Since(began)
	if err := ctx.Err(); err != nil {
		return ExecResult{}, fmt.Errorf("sandbox exec: %w", err)
	}
	state, err := s.cli.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return ExecResult{}, fmt.Errorf("sandbox exec: %w", err)
	}
	res := ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: state.ExitCode}
	// 137 alone could be an OOM kill, so the time must be up too.
	res.TimedOut = state.Running || (state.ExitCode == killedExitCode && elapsed >= timeout)
	if state.Running {
		res.ExitCode = killedExitCode
	}
	return res, nil
}

// withTimeout wraps cmd so that it is killed inside the container, because
// Docker has no API to kill an exec.
func withTimeout(cmd string, timeout time.Duration) []string {
	sec := strconv.Itoa(int(math.Ceil(timeout.Seconds())))
	return []string{"timeout", "-s", "KILL", sec, "sh", "-c", cmd}
}

// stream copies a command's output until it ends or ctx is done, which is not
// an error here.
func (s *Sandbox) stream(ctx context.Context, execID string, stdout, stderr *capture) error {
	conn, err := s.cli.ExecAttach(ctx, execID, client.ExecAttachOptions{})
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, conn.Close) // unblocks StdCopy
	defer stop()
	if _, err := stdcopy.StdCopy(stdout, stderr, conn.Reader); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}
