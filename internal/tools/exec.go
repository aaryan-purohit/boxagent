package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	defaultTimeoutSec = 60
	maxTimeoutSec     = 300
)

// ExecResult is the outcome of a command that ran.
type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	TimedOut bool   `json:"timed_out"`
}

// Executor runs shell commands in the sandbox.
type Executor interface {
	Exec(ctx context.Context, cmd string, timeout time.Duration) (ExecResult, error)
}

// Exec is the exec tool.
type Exec struct{ sandbox Executor }

// NewExec returns the exec tool.
func NewExec(sandbox Executor) *Exec { return &Exec{sandbox} }

// Name returns the tool name.
func (*Exec) Name() string { return "exec" }

// Description tells the model what the tool does.
func (*Exec) Description() string {
	return "Run a shell command (sh -c) in the sandbox. The working directory is /workspace."
}

// Schema returns the JSON Schema for the arguments.
func (*Exec) Schema() map[string]any {
	timeout := property("integer", fmt.Sprintf("Seconds before the command is killed. Default %d.", defaultTimeoutSec))
	timeout["minimum"] = 1
	timeout["maximum"] = maxTimeoutSec
	return object(map[string]any{
		"cmd":         property("string", "The shell command to run."),
		"timeout_sec": timeout,
	}, "cmd")
}

// Call runs the command. A non-zero exit code is a result, not an error.
func (t *Exec) Call(ctx context.Context, args json.RawMessage) (any, error) {
	in, err := decode[struct {
		Cmd        string `json:"cmd"`
		TimeoutSec *int   `json:"timeout_sec"`
	}](args)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Cmd) == "" {
		return nil, missing("cmd")
	}
	sec := defaultTimeoutSec
	if in.TimeoutSec != nil {
		sec = *in.TimeoutSec
	}
	if sec < 1 || sec > maxTimeoutSec {
		return nil, fmt.Errorf("%w: timeout_sec must be from 1 to %d, got %d", ErrInvalidArgs, maxTimeoutSec, sec)
	}
	res, err := t.sandbox.Exec(ctx, in.Cmd, time.Duration(sec)*time.Second)
	if err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}
	return res, nil
}
