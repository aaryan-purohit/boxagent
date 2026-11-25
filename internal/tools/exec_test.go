package tools

import (
	"errors"
	"testing"
	"time"
)

func TestExec(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, args  string
		result      ExecResult
		wantTimeout time.Duration
		want        string
	}{
		{
			"uses the default timeout", `{"cmd":"ls"}`, ExecResult{Stdout: "a"}, 60 * time.Second,
			`{"stdout":"a","stderr":"","exit_code":0,"timed_out":false}`,
		},
		{
			"non-zero exit is a result", `{"cmd":"ls","timeout_sec":1}`, ExecResult{Stderr: "bad", ExitCode: 2}, time.Second,
			`{"stdout":"","stderr":"bad","exit_code":2,"timed_out":false}`,
		},
		{
			"timeout is a result", `{"cmd":"ls","timeout_sec":300}`, ExecResult{ExitCode: 137, TimedOut: true}, 300 * time.Second,
			`{"stdout":"","stderr":"","exit_code":137,"timed_out":true}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sandbox := &fakeSandbox{result: tc.result}

			got, err := callJSON(t, NewExec(sandbox), tc.args)

			if err != nil {
				t.Fatalf("Call() error: %v", err)
			}
			diff(t, "result", tc.want, got)
			diff(t, "cmd", "ls", sandbox.cmd)
			diff(t, "timeout", tc.wantTimeout, sandbox.timeout)
		})
	}
}

func TestExecRejectsArgs(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, args string }{
		{"no args", ``},
		{"null args", `null`},
		{"missing cmd", `{}`},
		{"blank cmd", `{"cmd":" \n"}`},
		{"wrong cmd type", `{"cmd":1}`},
		{"wrong timeout type", `{"cmd":"ls","timeout_sec":"5"}`},
		{"fractional timeout", `{"cmd":"ls","timeout_sec":1.5}`},
		{"zero timeout", `{"cmd":"ls","timeout_sec":0}`},
		{"negative timeout", `{"cmd":"ls","timeout_sec":-1}`},
		{"timeout over the maximum", `{"cmd":"ls","timeout_sec":301}`},
		{"unknown field", `{"cmd":"ls","timeout":5}`},
		{"trailing data", `{"cmd":"ls"} {}`},
		{"malformed JSON", `{"cmd":`},
		{"not an object", `["ls"]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sandbox := &fakeSandbox{}

			_, err := callJSON(t, NewExec(sandbox), tc.args)

			if !errors.Is(err, ErrInvalidArgs) {
				t.Errorf("Call(%s) error = %v, want ErrInvalidArgs", tc.args, err)
			}
			diff(t, "cmd run", "", sandbox.cmd)
		})
	}
}

func TestExecReportsSandboxError(t *testing.T) {
	t.Parallel()
	if _, err := callJSON(t, NewExec(&fakeSandbox{err: errFake}), `{"cmd":"ls"}`); !errors.Is(err, errFake) {
		t.Errorf("Call() error = %v, want the sandbox error", err)
	}
}
