package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/moby/moby/client"
	"github.com/spf13/cobra"

	"github.com/aaryan-purohit/boxagent/internal/sandbox/docker"
)

type pruneFunc func(context.Context) ([]string, error)

func newSandboxCmd(prune pruneFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manage sandbox containers",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newSandboxPruneCmd(prune))
	return cmd
}

func newSandboxPruneCmd(prune pruneFunc) *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Remove all BoxAgent containers, including those of running sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			removed, err := prune(cmd.Context())
			for _, id := range removed {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), id) // the prune error matters more
			}
			return err
		},
	}
}

func pruneDocker(ctx context.Context) (removed []string, err error) {
	cli, err := newDockerClient()
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, cli.Close()) }()
	removed, err = docker.Prune(ctx, cli)
	return removed, explainDocker(err)
}

// explainDocker adds advice to an error about an unreachable Docker daemon.
func explainDocker(err error) error {
	if !client.IsErrConnectionFailed(err) {
		return err
	}
	return fmt.Errorf("%w\nStart Docker. If it is running, set DOCKER_HOST to its address; Docker contexts are ignored. To find it:\n"+
		"  docker context inspect --format '{{.Endpoints.docker.Host}}'", err)
}

// newDockerClient honours Docker's own environment variables (DOCKER_HOST and
// friends), which belong to the Docker library rather than internal/config.
func newDockerClient() (*client.Client, error) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return cli, nil
}
