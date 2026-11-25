package tools

import (
	"errors"
	"testing"
	"time"

	"github.com/aaryan-purohit/boxagent/internal/agent"
)

func TestVFSTools(t *testing.T) {
	t.Parallel()
	versions := []FileVersion{
		{Path: "a", Version: 2, Hash: "h2", Size: 3, CreatedAt: time.Date(2026, 1, 2, 5, 4, 5, 0, time.FixedZone("X", 7200))},
		{Path: "b", Version: 1, Hash: "h1", Size: 0, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	tests := []struct {
		name     string
		tool     func(*fakeFiles) agent.Tool
		files    fakeFiles
		args     string
		wantCall string
		want     string
	}{
		{
			"write saves content", write, fakeFiles{}, `{"path":"a","content":"hi"}`,
			"write a=hi", `{"path":"a","version":1,"hash":"h"}`,
		},
		{
			"write allows empty content", write, fakeFiles{}, `{"path":"a","content":""}`,
			"write a=", `{"path":"a","version":1,"hash":"h"}`,
		},
		{
			"read defaults to the latest", read, fakeFiles{content: "héllo"}, `{"path":"a"}`,
			"read a@0", `{"content":"héllo","version":7}`,
		},
		{
			"read takes a version", read, fakeFiles{}, `{"path":"a","version":3}`,
			"read a@3", `{"content":"","version":7}`,
		},
		{
			"read describes invalid UTF-8", read, fakeFiles{content: "\xff\xfe"}, `{"path":"a"}`,
			"read a@0", `{"content":"binary file, 2 bytes","version":7}`,
		},
		{
			"read describes content with NUL", read, fakeFiles{content: "a\x00b"}, `{"path":"a"}`,
			"read a@0", `{"content":"binary file, 3 bytes","version":7}`,
		},
		{
			"list defaults to everything", list, fakeFiles{versions: versions}, ``,
			"list ", `[{"path":"a","latest_version":2,"size":3},{"path":"b","latest_version":1,"size":0}]`,
		},
		{"list takes a prefix", list, fakeFiles{}, `{"prefix":"src"}`, "list src", `[]`},
		{
			"history returns versions in UTC", history, fakeFiles{versions: versions}, `{"path":"a"}`,
			"history a",
			`[{"version":2,"hash":"h2","size":3,"created_at":"2026-01-02T03:04:05Z"},` +
				`{"version":1,"hash":"h1","size":0,"created_at":"2026-01-01T00:00:00Z"}]`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := callJSON(t, tc.tool(&tc.files), tc.args)

			if err != nil {
				t.Fatalf("Call() error: %v", err)
			}
			diff(t, "result", tc.want, got)
			diff(t, "store calls", []string{tc.wantCall}, tc.files.log)
		})
	}
}

func TestVFSToolsRejectArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tool func(*fakeFiles) agent.Tool
		args string
	}{
		{"write without path", write, `{"content":"x"}`},
		{"write with empty path", write, `{"path":"","content":"x"}`},
		{"write without content", write, `{"path":"a"}`},
		{"write with wrong content type", write, `{"path":"a","content":1}`},
		{"write with unknown field", write, `{"path":"a","content":"x","mode":1}`},
		{"read without path", read, `{}`},
		{"read with wrong path type", read, `{"path":1}`},
		{"read with version zero", read, `{"path":"a","version":0}`},
		{"read with negative version", read, `{"path":"a","version":-1}`},
		{"read with wrong version type", read, `{"path":"a","version":"1"}`},
		{"list with wrong prefix type", list, `{"prefix":1}`},
		{"list with unknown field", list, `{"path":"a"}`},
		{"history without path", history, `{}`},
		{"history with wrong path type", history, `{"path":[]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files := &fakeFiles{}

			_, err := callJSON(t, tc.tool(files), tc.args)

			if !errors.Is(err, ErrInvalidArgs) {
				t.Errorf("Call(%s) error = %v, want ErrInvalidArgs", tc.args, err)
			}
			if len(files.log) != 0 {
				t.Errorf("store was called: %q", files.log)
			}
		})
	}
}

func TestVFSToolsReportStoreErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tool func(*fakeFiles) agent.Tool
		args string
	}{
		{"write", write, `{"path":"a","content":"x"}`},
		{"read", read, `{"path":"a"}`},
		{"list", list, `{}`},
		{"history", history, `{"path":"a"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := callJSON(t, tc.tool(&fakeFiles{err: errFake}), tc.args); !errors.Is(err, errFake) {
				t.Errorf("Call() error = %v, want the store error", err)
			}
		})
	}
}

func write(f *fakeFiles) agent.Tool   { return NewVFSWrite(f) }
func read(f *fakeFiles) agent.Tool    { return NewVFSRead(f) }
func list(f *fakeFiles) agent.Tool    { return NewVFSList(f) }
func history(f *fakeFiles) agent.Tool { return NewVFSHistory(f) }
