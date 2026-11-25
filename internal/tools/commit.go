package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// maxCommitPaths bounds how much content one call holds in memory.
const maxCommitPaths = 100

// SandboxReader reads a file from the sandbox's /workspace.
type SandboxReader interface {
	CopyOut(ctx context.Context, path string) ([]byte, error)
}

// VFSCommit is the vfs_commit tool.
type VFSCommit struct {
	sandbox SandboxReader
	files   FileWriter
}

// NewVFSCommit returns the vfs_commit tool.
func NewVFSCommit(sandbox SandboxReader, files FileWriter) *VFSCommit {
	return &VFSCommit{sandbox, files}
}

// Name returns the tool name.
func (*VFSCommit) Name() string { return "vfs_commit" }

// Description tells the model what the tool does.
func (*VFSCommit) Description() string {
	return "Save files from /workspace to the VFS under the same relative paths. List files, not directories."
}

// Schema returns the JSON Schema for the arguments.
func (*VFSCommit) Schema() map[string]any {
	paths := property("array", "Files to save, relative to /workspace.")
	paths["items"] = map[string]any{"type": "string"}
	paths["minItems"] = 1
	paths["maxItems"] = maxCommitPaths
	return object(map[string]any{"paths": paths}, "paths")
}

type committed struct {
	Path    string `json:"path"`
	Version int    `json:"version"`
}

// Call copies every file out of the sandbox before saving any, so an
// unreadable file fails the call before a version is created.
func (t *VFSCommit) Call(ctx context.Context, args json.RawMessage) (any, error) {
	in, err := decode[struct {
		Paths []string `json:"paths"`
	}](args)
	if err != nil {
		return nil, err
	}
	if len(in.Paths) == 0 {
		return nil, missing("paths")
	}
	if len(in.Paths) > maxCommitPaths {
		return nil, fmt.Errorf("%w: at most %d paths per call, got %d", ErrInvalidArgs, maxCommitPaths, len(in.Paths))
	}
	contents := make([][]byte, len(in.Paths))
	for i, p := range in.Paths {
		if contents[i], err = t.sandbox.CopyOut(ctx, p); err != nil {
			return nil, fmt.Errorf("vfs_commit: nothing saved: copy %q: %w", p, err)
		}
	}
	out := make([]committed, 0, len(in.Paths))
	for i, p := range in.Paths {
		v, err := t.files.Write(ctx, p, contents[i])
		if err != nil {
			return nil, fmt.Errorf("vfs_commit: saved the first %d of %d files: save %q: %w", i, len(in.Paths), p, err)
		}
		out = append(out, committed{v.Path, v.Version})
	}
	return out, nil
}
