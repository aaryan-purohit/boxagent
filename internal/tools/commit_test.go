package tools

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestVFSCommit(t *testing.T) {
	t.Parallel()
	sandbox := &fakeSandbox{files: map[string]string{"a": "1", "b": "2"}}
	files := &fakeFiles{}

	got, err := callJSON(t, NewVFSCommit(sandbox, files), `{"paths":["a","b"]}`)

	if err != nil {
		t.Fatalf("Call() error: %v", err)
	}
	diff(t, "result", `[{"path":"a","version":1},{"path":"b","version":1}]`, got)
	diff(t, "store calls", []string{"write a=1", "write b=2"}, files.log)
}

func TestVFSCommitSavesNothingIfAFileIsUnreadable(t *testing.T) {
	t.Parallel()
	files := &fakeFiles{}

	_, err := callJSON(t, NewVFSCommit(&fakeSandbox{files: map[string]string{"a": "1"}}, files), `{"paths":["a","gone"]}`)

	if !errors.Is(err, errFake) {
		t.Errorf("Call() error = %v, want the sandbox error", err)
	}
	if len(files.log) != 0 {
		t.Errorf("store was called: %q", files.log)
	}
}

func TestVFSCommitReportsPartialSave(t *testing.T) {
	t.Parallel()
	sandbox := &fakeSandbox{files: map[string]string{"a": "1", "b": "2"}}

	_, err := callJSON(t, NewVFSCommit(sandbox, &fakeFiles{failPath: "b"}), `{"paths":["a","b"]}`)

	if !errors.Is(err, errFake) {
		t.Fatalf("Call() error = %v, want the store error", err)
	}
	// The message is the model's only way to learn what was saved.
	if want := "saved the first 1 of 2 files"; !strings.Contains(err.Error(), want) {
		t.Errorf("Call() error = %q, want it to say %q", err, want)
	}
}

func TestVFSCommitPathLimit(t *testing.T) {
	t.Parallel()
	args := func(n int) string {
		raw, err := json.Marshal(map[string]any{"paths": slices.Repeat([]string{"a"}, n)})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	tool := NewVFSCommit(&fakeSandbox{files: map[string]string{"a": "1"}}, &fakeFiles{})

	if _, err := callJSON(t, tool, args(100)); err != nil {
		t.Errorf("Call() at the limit error: %v", err)
	}
	if _, err := callJSON(t, tool, args(101)); !errors.Is(err, ErrInvalidArgs) {
		t.Errorf("Call() over the limit error = %v, want ErrInvalidArgs", err)
	}
}

func TestVFSCommitRejectsArgs(t *testing.T) {
	t.Parallel()
	for _, args := range []string{``, `{}`, `{"paths":[]}`, `{"paths":"a"}`, `{"paths":[1]}`, `{"paths":["a"],"x":1}`} {
		_, err := callJSON(t, NewVFSCommit(&fakeSandbox{}, &fakeFiles{}), args)
		if !errors.Is(err, ErrInvalidArgs) {
			t.Errorf("Call(%s) error = %v, want ErrInvalidArgs", args, err)
		}
	}
}
