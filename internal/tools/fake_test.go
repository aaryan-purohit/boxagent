package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/aaryan-purohit/boxagent/internal/agent"
)

var errFake = errors.New("fake failure")

// fakeSandbox records its calls. Files missing from files fail to copy.
type fakeSandbox struct {
	result  ExecResult
	err     error
	files   map[string]string
	cmd     string
	timeout time.Duration
}

func (s *fakeSandbox) Exec(_ context.Context, cmd string, timeout time.Duration) (ExecResult, error) {
	s.cmd, s.timeout = cmd, timeout
	return s.result, s.err
}

func (s *fakeSandbox) CopyOut(_ context.Context, path string) ([]byte, error) {
	content, ok := s.files[path]
	if !ok {
		return nil, errFake
	}
	return []byte(content), nil
}

// fakeFiles returns canned versions and records its calls as strings. A write
// to failPath fails.
type fakeFiles struct {
	versions []FileVersion
	content  string
	err      error
	failPath string
	log      []string
}

func (f *fakeFiles) Write(_ context.Context, path string, content []byte) (FileVersion, error) {
	f.log = append(f.log, fmt.Sprintf("write %s=%s", path, content))
	if path == f.failPath {
		return FileVersion{}, errFake
	}
	return FileVersion{Path: path, Version: 1, Hash: "h"}, f.err
}

func (f *fakeFiles) Read(_ context.Context, path string, version int) ([]byte, FileVersion, error) {
	f.log = append(f.log, fmt.Sprintf("read %s@%d", path, version))
	return []byte(f.content), FileVersion{Path: path, Version: 7}, f.err
}

func (f *fakeFiles) List(_ context.Context, prefix string) ([]FileVersion, error) {
	f.log = append(f.log, "list "+prefix)
	return f.versions, f.err
}

func (f *fakeFiles) History(_ context.Context, path string) ([]FileVersion, error) {
	f.log = append(f.log, "history "+path)
	return f.versions, f.err
}

// callJSON calls the tool and returns its result as JSON, as the model sees it.
func callJSON(t *testing.T, tool agent.Tool, args string) (string, error) {
	t.Helper()
	out, err := tool.Call(t.Context(), json.RawMessage(args))
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("result does not encode: %v", err)
	}
	return string(raw), nil
}

func diff(t *testing.T, what string, want, got any) {
	t.Helper()
	if d := cmp.Diff(want, got); d != "" {
		t.Errorf("%s mismatch (-want +got):\n%s", what, d)
	}
}
