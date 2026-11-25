package docker

import (
	"context"
	"errors"
	"fmt"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
)

const (
	maxDiskBytes = 4 << 30
	diskInterval = time.Second
)

// ErrDiskLimit means the sandbox wrote too much data and was destroyed.
var ErrDiskLimit = errors.New("sandbox exceeded its disk limit")

// watchDisk starts checking disk use in the background. Few Docker storage
// drivers can enforce a quota, so past limit it calls onLimit and kills the
// container. Destroy stops it.
func (s *Sandbox) watchDisk(ctx context.Context, limit int64, onLimit func(error)) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	s.stopWatch = func() {
		cancel()
		<-done
	}
	go func() {
		defer close(done)
		if err := s.waitForLimit(ctx, limit); errors.Is(err, ErrDiskLimit) {
			onLimit(err)
			_, _ = s.cli.ContainerKill(ctx, s.id, client.ContainerKillOptions{}) // Destroy removes it anyway
		}
	}()
}

// waitForLimit returns ErrDiskLimit once the container has written more than
// limit bytes, or another error when there is nothing left to watch.
func (s *Sandbox) waitForLimit(ctx context.Context, limit int64) error {
	ticker := time.NewTicker(diskInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		res, err := s.cli.ContainerInspect(ctx, s.id, client.ContainerInspectOptions{Size: true})
		if cerrdefs.IsNotFound(err) {
			return err
		}
		if err != nil || res.Container.SizeRw == nil {
			continue // transient; try again
		}
		if used := *res.Container.SizeRw; used > limit {
			return fmt.Errorf("%w: wrote %d bytes, limit %d", ErrDiskLimit, used, limit)
		}
	}
}
