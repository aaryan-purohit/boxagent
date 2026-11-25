package cli

import (
	"context"
	"time"

	"github.com/aaryan-purohit/boxagent/internal/sandbox/docker"
	"github.com/aaryan-purohit/boxagent/internal/tools"
	"github.com/aaryan-purohit/boxagent/internal/vfs"
)

// sessionFiles gives tools the VFS: it validates the model's paths and stamps
// writes with the session ID.
type sessionFiles struct {
	store     *vfs.Store
	sessionID string
}

func (f sessionFiles) Write(ctx context.Context, path string, content []byte) (tools.FileVersion, error) {
	p, err := vfs.ParsePath(path)
	if err != nil {
		return tools.FileVersion{}, err
	}
	v, err := f.store.Write(ctx, p, content, f.sessionID)
	return toFileVersion(v), err
}

func (f sessionFiles) Read(ctx context.Context, path string, version int) ([]byte, tools.FileVersion, error) {
	p, err := vfs.ParsePath(path)
	if err != nil {
		return nil, tools.FileVersion{}, err
	}
	content, v, err := f.store.Read(ctx, p, version)
	return content, toFileVersion(v), err
}

func (f sessionFiles) List(ctx context.Context, prefix string) ([]tools.FileVersion, error) {
	p, err := vfs.ParsePrefix(prefix)
	if err != nil {
		return nil, err
	}
	vs, err := f.store.List(ctx, p)
	return toFileVersions(vs), err
}

func (f sessionFiles) History(ctx context.Context, path string) ([]tools.FileVersion, error) {
	p, err := vfs.ParsePath(path)
	if err != nil {
		return nil, err
	}
	vs, err := f.store.History(ctx, p)
	return toFileVersions(vs), err
}

func toFileVersions(vs []vfs.Version) []tools.FileVersion {
	out := make([]tools.FileVersion, 0, len(vs))
	for _, v := range vs {
		out = append(out, toFileVersion(v))
	}
	return out
}

func toFileVersion(v vfs.Version) tools.FileVersion {
	return tools.FileVersion{Path: v.Path, Version: v.Version, Hash: v.Hash, Size: v.Size, CreatedAt: v.CreatedAt}
}

// container is the part of docker.Sandbox that tools use.
type container interface {
	Exec(ctx context.Context, cmd string, timeout time.Duration) (docker.ExecResult, error)
	CopyOut(ctx context.Context, path string) ([]byte, error)
}

// sandboxTools gives tools the sandbox: it validates the model's paths.
type sandboxTools struct{ container container }

func (s sandboxTools) Exec(ctx context.Context, cmd string, timeout time.Duration) (tools.ExecResult, error) {
	res, err := s.container.Exec(ctx, cmd, timeout)
	return tools.ExecResult(res), err
}

func (s sandboxTools) CopyOut(ctx context.Context, path string) ([]byte, error) {
	p, err := vfs.ParsePath(path)
	if err != nil {
		return nil, err
	}
	return s.container.CopyOut(ctx, p.String())
}
