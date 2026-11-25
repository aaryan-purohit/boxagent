// Package cli defines the BoxAgent commands and wires their dependencies.
package cli

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/aaryan-purohit/boxagent/internal/config"
	"github.com/aaryan-purohit/boxagent/internal/vfs"
)

// Execute runs the BoxAgent CLI, cancelling its context on a termination
// signal. SIGHUP (closed terminal) and SIGPIPE (closed output pipe) are caught
// too, because their default action would skip sandbox cleanup.
func Execute(version string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGPIPE)
	defer stop()
	return newRootCmd(version, os.Getenv).ExecuteContext(ctx)
}

func newRootCmd(version string, getenv func(string) string) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "boxagent",
		Short:        "A sandboxed coding agent",
		Version:      version,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE:         func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newVFSCmd(func(ctx context.Context) (*vfs.Store, error) {
		cfg, err := config.Load(getenv)
		if err != nil {
			return nil, err
		}
		return vfs.Open(ctx, cfg.DataDir, time.Now)
	}))
	cmd.AddCommand(newSandboxCmd(pruneDocker))
	cmd.AddCommand(newRunCmd(getenv, newGemini))
	return cmd
}

// newLogger logs to w, which is stderr in production so stdout stays clean.
func newLogger(w io.Writer, cfg config.Config) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: cfg.LogLevel}))
}
