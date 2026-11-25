// Package docker runs an agent's commands in a locked-down Docker container.
package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

const (
	sessionLabel = "boxagent.session"
	workspace    = "/workspace"
	home         = "/home/agent"
	uid          = 1000

	nanoCPUs    = 1_000_000_000 // 1 CPU
	memoryBytes = 1 << 30
	pidsLimit   = 256

	createTimeout  = time.Minute
	cleanupTimeout = 15 * time.Second
	checkTimeout   = 10 * time.Second
)

var errUnusableImage = errors.New("sandbox cannot run commands; the image must provide sleep, timeout and sh")

// Options configures a Sandbox.
type Options struct {
	Image     string
	SessionID string
	Network   bool // allow network access
	Log       *slog.Logger
	// OnDiskLimit is called from another goroutine, with ErrDiskLimit, if the
	// sandbox is killed for writing too much.
	OnDiskLimit func(error)
}

// Sandbox is a running container. Call Destroy when done with it.
type Sandbox struct {
	cli       *client.Client
	id        string
	stopWatch func()
}

// New pulls the image if needed and starts a container. On error no container
// is left behind.
func New(ctx context.Context, cli *client.Client, opts Options) (*Sandbox, error) {
	if err := ensureImage(ctx, cli, opts); err != nil {
		return nil, fmt.Errorf("sandbox image %q: %w", opts.Image, err)
	}
	s, err := create(ctx, cli, opts)
	if err != nil {
		return nil, fmt.Errorf("create sandbox: %w", err)
	}
	if err := s.start(ctx); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		return nil, errors.Join(fmt.Errorf("start sandbox: %w", err), s.Destroy(cleanupCtx))
	}
	s.watchDisk(ctx, maxDiskBytes, opts.OnDiskLimit)
	return s, nil
}

// Destroy kills and removes the container. Destroying twice is not an error.
func (s *Sandbox) Destroy(ctx context.Context) error {
	s.stopWatch()
	if err := remove(ctx, s.cli, s.id); err != nil {
		return fmt.Errorf("destroy sandbox: %w", err)
	}
	return nil
}

// Prune removes every BoxAgent container, including those of live sessions,
// and returns the IDs it removed.
func Prune(ctx context.Context, cli *client.Client) ([]string, error) {
	found, err := cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", sessionLabel),
	})
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}
	var removed []string
	var errs []error
	for _, c := range found.Items {
		if err := remove(ctx, cli, c.ID); err != nil {
			errs = append(errs, fmt.Errorf("remove sandbox: %w", err))
			continue
		}
		removed = append(removed, c.ID)
	}
	return removed, errors.Join(errs...)
}

func remove(ctx context.Context, cli *client.Client, id string) error {
	_, err := cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}

func ensureImage(ctx context.Context, cli *client.Client, opts Options) error {
	_, err := cli.ImageInspect(ctx, opts.Image)
	if !cerrdefs.IsNotFound(err) {
		return err
	}
	opts.Log.Info("pulling sandbox image", "image", opts.Image)
	progress, err := cli.ImagePull(ctx, opts.Image, client.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = progress.Close() }() // read-only, error irrelevant
	return progress.Wait(ctx)
}

// create ignores cancellation of ctx: the daemon could otherwise create a
// container whose ID we never receive and so can't remove.
func create(ctx context.Context, cli *client.Client, opts Options) (*Sandbox, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	createCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), createTimeout)
	defer cancel()
	resp, err := cli.ContainerCreate(createCtx, client.ContainerCreateOptions{
		Config:     containerConfig(opts),
		HostConfig: hostConfig(opts),
	})
	if err != nil {
		return nil, err
	}
	return &Sandbox{cli: cli, id: resp.ID, stopWatch: func() {}}, nil
}

func containerConfig(opts Options) *container.Config {
	return &container.Config{
		Image:       opts.Image,
		Entrypoint:  []string{"sleep", "infinity"},
		User:        fmt.Sprintf("%d:%d", uid, uid),
		WorkingDir:  workspace,
		Env:         []string{"HOME=" + home, "GOTOOLCHAIN=local"},
		Labels:      map[string]string{sessionLabel: opts.SessionID},
		Healthcheck: &container.HealthConfig{Test: []string{"NONE"}},
	}
}

func hostConfig(opts Options) *container.HostConfig {
	pids, reapZombies := int64(pidsLimit), true
	network := container.NetworkMode("none")
	if opts.Network {
		network = "bridge"
	}
	return &container.HostConfig{
		NetworkMode: network,
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges"},
		Init:        &reapZombies,
		Resources: container.Resources{
			NanoCPUs:   nanoCPUs,
			Memory:     memoryBytes,
			MemorySwap: memoryBytes, // equal to Memory: no swap
			PidsLimit:  &pids,
		},
	}
}

// start creates the directories the non-root user needs, then starts the
// container.
func (s *Sandbox) start(ctx context.Context) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, dir := range []string{workspace[1:], home[1:]} {
		if err := tw.WriteHeader(dirHeader(dir, time.Now())); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if _, err := s.cli.CopyToContainer(ctx, s.id, client.CopyToContainerOptions{DestinationPath: "/", Content: &buf}); err != nil {
		return err
	}
	if _, err := s.cli.ContainerStart(ctx, s.id, client.ContainerStartOptions{}); err != nil {
		return err
	}
	return s.check(ctx)
}

// check fails if the image lacks the programs a sandbox needs. Docker's init
// process hides that from ContainerStart.
func (s *Sandbox) check(ctx context.Context) error {
	res, err := s.Exec(ctx, "true", checkTimeout)
	if err == nil && res.ExitCode == 0 {
		return nil
	}
	return errors.Join(errUnusableImage, err)
}
