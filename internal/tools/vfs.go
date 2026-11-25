package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"
)

// FileVersion describes one stored version of a file.
type FileVersion struct {
	Path      string
	Version   int
	Hash      string
	Size      int64
	CreatedAt time.Time
}

// FileWriter saves a new version of a file.
type FileWriter interface {
	Write(ctx context.Context, path string, content []byte) (FileVersion, error)
}

// FileReader reads a version of a file. Version 0 means the latest.
type FileReader interface {
	Read(ctx context.Context, path string, version int) ([]byte, FileVersion, error)
}

// FileLister lists the latest version of files. An empty prefix means all.
type FileLister interface {
	List(ctx context.Context, prefix string) ([]FileVersion, error)
}

// FileHistorian lists every version of a file, newest first.
type FileHistorian interface {
	History(ctx context.Context, path string) ([]FileVersion, error)
}

// VFSWrite is the vfs_write tool.
type VFSWrite struct{ files FileWriter }

// NewVFSWrite returns the vfs_write tool.
func NewVFSWrite(files FileWriter) *VFSWrite { return &VFSWrite{files} }

// Name returns the tool name.
func (*VFSWrite) Name() string { return "vfs_write" }

// Description tells the model what the tool does.
func (*VFSWrite) Description() string {
	return "Save text directly to the VFS as a new version. The file does not appear in /workspace, so commands cannot use it; create files with exec and save them with vfs_commit instead. Saving the same content as the latest version changes nothing."
}

// Schema returns the JSON Schema for the arguments.
func (*VFSWrite) Schema() map[string]any {
	return object(map[string]any{
		"path":    property("string", "Relative VFS path, such as src/main.go."),
		"content": property("string", "The full content of the file."),
	}, "path", "content")
}

// Call saves the file.
func (t *VFSWrite) Call(ctx context.Context, args json.RawMessage) (any, error) {
	in, err := decode[struct {
		Path    string  `json:"path"`
		Content *string `json:"content"` // pointer: empty content is valid, missing is not
	}](args)
	if err != nil {
		return nil, err
	}
	if in.Path == "" {
		return nil, missing("path")
	}
	if in.Content == nil {
		return nil, missing("content")
	}
	v, err := t.files.Write(ctx, in.Path, []byte(*in.Content))
	if err != nil {
		return nil, fmt.Errorf("vfs_write: %w", err)
	}
	return struct {
		Path    string `json:"path"`
		Version int    `json:"version"`
		Hash    string `json:"hash"`
	}{v.Path, v.Version, v.Hash}, nil
}

// VFSRead is the vfs_read tool.
type VFSRead struct{ files FileReader }

// NewVFSRead returns the vfs_read tool.
func NewVFSRead(files FileReader) *VFSRead { return &VFSRead{files} }

// Name returns the tool name.
func (*VFSRead) Name() string { return "vfs_read" }

// Description tells the model what the tool does.
func (*VFSRead) Description() string {
	return "Read a text file from the VFS. Binary files return only a note with their size."
}

// Schema returns the JSON Schema for the arguments.
func (*VFSRead) Schema() map[string]any {
	version := property("integer", "Version to read. Default is the latest.")
	version["minimum"] = 1
	return object(map[string]any{
		"path":    property("string", "Relative VFS path."),
		"version": version,
	}, "path")
}

// Call reads the file.
func (t *VFSRead) Call(ctx context.Context, args json.RawMessage) (any, error) {
	in, err := decode[struct {
		Path    string `json:"path"`
		Version *int   `json:"version"`
	}](args)
	if err != nil {
		return nil, err
	}
	if in.Path == "" {
		return nil, missing("path")
	}
	version := 0
	if in.Version != nil {
		version = *in.Version
		if version < 1 {
			return nil, fmt.Errorf("%w: version must be at least 1, got %d", ErrInvalidArgs, version)
		}
	}
	content, v, err := t.files.Read(ctx, in.Path, version)
	if err != nil {
		return nil, fmt.Errorf("vfs_read: %w", err)
	}
	return struct {
		Content string `json:"content"`
		Version int    `json:"version"`
	}{describe(content), v.Version}, nil
}

// describe returns text content unchanged and replaces binary with a note.
func describe(content []byte) string {
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return fmt.Sprintf("binary file, %d bytes", len(content))
	}
	return string(content)
}

// VFSList is the vfs_list tool.
type VFSList struct{ files FileLister }

// NewVFSList returns the vfs_list tool.
func NewVFSList(files FileLister) *VFSList { return &VFSList{files} }

// Name returns the tool name.
func (*VFSList) Name() string { return "vfs_list" }

// Description tells the model what the tool does.
func (*VFSList) Description() string {
	return "List VFS files with their latest version and size."
}

// Schema returns the JSON Schema for the arguments.
func (*VFSList) Schema() map[string]any {
	return object(map[string]any{
		"prefix": property("string", "File or directory to list. Default is everything."),
	})
}

// Call lists the files.
func (t *VFSList) Call(ctx context.Context, args json.RawMessage) (any, error) {
	in, err := decode[struct {
		Prefix string `json:"prefix"`
	}](args)
	if err != nil {
		return nil, err
	}
	files, err := t.files.List(ctx, in.Prefix)
	if err != nil {
		return nil, fmt.Errorf("vfs_list: %w", err)
	}
	type item struct {
		Path          string `json:"path"`
		LatestVersion int    `json:"latest_version"`
		Size          int64  `json:"size"`
	}
	out := make([]item, 0, len(files)) // encodes as [] when empty
	for _, f := range files {
		out = append(out, item{f.Path, f.Version, f.Size})
	}
	return out, nil
}

// VFSHistory is the vfs_history tool.
type VFSHistory struct{ files FileHistorian }

// NewVFSHistory returns the vfs_history tool.
func NewVFSHistory(files FileHistorian) *VFSHistory { return &VFSHistory{files} }

// Name returns the tool name.
func (*VFSHistory) Name() string { return "vfs_history" }

// Description tells the model what the tool does.
func (*VFSHistory) Description() string {
	return "List every version of a VFS file, newest first."
}

// Schema returns the JSON Schema for the arguments.
func (*VFSHistory) Schema() map[string]any {
	return object(map[string]any{
		"path": property("string", "Relative VFS path."),
	}, "path")
}

// Call lists the versions.
func (t *VFSHistory) Call(ctx context.Context, args json.RawMessage) (any, error) {
	in, err := decode[struct {
		Path string `json:"path"`
	}](args)
	if err != nil {
		return nil, err
	}
	if in.Path == "" {
		return nil, missing("path")
	}
	versions, err := t.files.History(ctx, in.Path)
	if err != nil {
		return nil, fmt.Errorf("vfs_history: %w", err)
	}
	type item struct {
		Version   int    `json:"version"`
		Hash      string `json:"hash"`
		Size      int64  `json:"size"`
		CreatedAt string `json:"created_at"`
	}
	out := make([]item, 0, len(versions))
	for _, v := range versions {
		out = append(out, item{v.Version, v.Hash, v.Size, v.CreatedAt.UTC().Format(time.RFC3339)})
	}
	return out, nil
}
