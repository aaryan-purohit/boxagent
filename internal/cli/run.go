package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/moby/moby/client"
	"github.com/spf13/cobra"

	"github.com/aaryan-purohit/boxagent/internal/agent"
	"github.com/aaryan-purohit/boxagent/internal/config"
	"github.com/aaryan-purohit/boxagent/internal/llm/gemini"
	"github.com/aaryan-purohit/boxagent/internal/sandbox/docker"
	"github.com/aaryan-purohit/boxagent/internal/session"
	"github.com/aaryan-purohit/boxagent/internal/tools"
	"github.com/aaryan-purohit/boxagent/internal/vfs"
)

var (
	errEmptyTask = errors.New("the task is empty")
	errEmptySeed = errors.New("no files to seed")
	errNoTask    = errors.New("no task given")
	errTurnLimit = errors.New("stopped at the turn limit before finishing; raise it with --max-turns")
)

type newModelFunc func(context.Context, config.Config, *slog.Logger) (agent.Model, error)

// runOptions are the settings of one run.
type runOptions struct {
	task     string
	seed     string
	hasSeed  bool
	maxTurns int
	network  bool
	out      io.Writer
	logOut   io.Writer
}

func newRunCmd(getenv func(string) string, newModel newModelFunc) *cobra.Command {
	var opts runOptions
	cmd := &cobra.Command{
		Use:   "run <task>",
		Short: "Work on a task in a fresh sandbox",
		Args:  runArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(getenv)
			if err != nil {
				return err
			}
			if err := cfg.RequireAPIKey(); err != nil {
				return err
			}
			if !cmd.Flags().Changed("max-turns") {
				opts.maxTurns = cfg.MaxTurns
			}
			opts.task, opts.hasSeed = args[0], cmd.Flags().Changed("seed")
			opts.out, opts.logOut = cmd.OutOrStdout(), cmd.ErrOrStderr()
			return run(cmd.Context(), cfg, opts, newModel)
		},
	}
	cmd.Flags().StringVar(&opts.seed, "seed", "", "VFS file or directory to copy into /workspace first (. for everything)")
	cmd.Flags().IntVar(&opts.maxTurns, "max-turns", 0, "turn limit (default from BOXAGENT_MAX_TURNS)")
	cmd.Flags().BoolVar(&opts.network, "network", false, "allow network access from the sandbox")
	return cmd
}

// runArgs requires one task, and explains the likeliest way to miss it.
func runArgs(cmd *cobra.Command, args []string) error {
	if seed := cmd.Flags().Lookup("seed"); len(args) == 0 && seed.Changed {
		return fmt.Errorf("%w: --seed took %q as its path; use: run --seed <path> \"<task>\"", errNoTask, seed.Value)
	}
	return cobra.ExactArgs(1)(cmd, args)
}

func newGemini(ctx context.Context, cfg config.Config, log *slog.Logger) (agent.Model, error) {
	return gemini.New(ctx, gemini.Options{APIKey: cfg.APIKey, Model: cfg.Model, Log: log})
}

// run opens everything a task needs, works on it and closes everything again.
func run(ctx context.Context, cfg config.Config, opts runOptions, newModel newModelFunc) (err error) {
	if strings.TrimSpace(opts.task) == "" {
		return errEmptyTask
	}
	if opts.maxTurns < 1 {
		return fmt.Errorf("%w, got %d", agent.ErrInvalidMaxTurns, opts.maxTurns)
	}
	store, err := vfs.Open(ctx, cfg.DataDir, time.Now)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	seed, err := seedFiles(ctx, store, opts)
	if err != nil {
		return err
	}
	log := newLogger(opts.logOut, cfg)
	model, err := newModel(ctx, cfg, log)
	if err != nil {
		return err
	}
	dockerClient, err := newDockerClient()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dockerClient.Close()) }()
	deps := runDeps{store: store, seed: seed, model: model, docker: dockerClient, log: log}
	return explainDocker(runSession(ctx, cfg, opts, deps))
}

// runDeps are the open resources of one run.
type runDeps struct {
	store  *vfs.Store
	seed   []session.SeedFile
	model  agent.Model
	docker *client.Client
	log    *slog.Logger
}

// runSession wires the sandbox, tools and agent together for one task.
func runSession(ctx context.Context, cfg config.Config, opts runOptions, deps runDeps) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	id := session.NewID()
	deps.log.Info("session started", "session", id, "model", cfg.Model, "image", cfg.Image, "seeded", len(deps.seed))
	res, err := session.Run(ctx, session.Options[*docker.Sandbox]{
		Task: opts.task,
		Seed: deps.seed,
		Create: func(ctx context.Context) (*docker.Sandbox, error) {
			return docker.New(ctx, deps.docker, docker.Options{
				Image: cfg.Image, SessionID: id, Network: opts.network, Log: deps.log, OnDiskLimit: cancel,
			})
		},
		NewAgent: func(sb *docker.Sandbox) (session.Runner, error) {
			return newAgent(deps.model, sandboxTools{sb}, sessionFiles{deps.store, id}, opts)
		},
	})
	deps.log.Info("session ended", "session", id, "turns", res.Turns)
	if cause := context.Cause(ctx); errors.Is(cause, docker.ErrDiskLimit) {
		return cause // says more than the cancellation it led to
	}
	if err == nil && res.HitLimit {
		return errTurnLimit
	}
	return err
}

func newAgent(model agent.Model, sandbox sandboxTools, files sessionFiles, opts runOptions) (*agent.Agent, error) {
	registry, err := agent.NewRegistry(
		tools.NewExec(sandbox),
		tools.NewVFSWrite(files),
		tools.NewVFSRead(files),
		tools.NewVFSList(files),
		tools.NewVFSHistory(files),
		tools.NewVFSCommit(sandbox, files),
	)
	if err != nil {
		return nil, err
	}
	return agent.New(agent.Options{
		Model:    model,
		Tools:    registry,
		MaxTurns: opts.maxTurns,
		OnEvent:  func(e agent.Event) { printEvent(opts.out, e) },
	})
}

// seedFiles finds the files to seed. It runs before the sandbox is created, so
// a mistyped prefix fails fast.
func seedFiles(ctx context.Context, store *vfs.Store, opts runOptions) ([]session.SeedFile, error) {
	if !opts.hasSeed {
		return nil, nil
	}
	prefix, err := parsePrefix(opts.seed)
	if err != nil {
		return nil, err
	}
	found, err := store.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("%w under %q", errEmptySeed, opts.seed)
	}
	files := make([]session.SeedFile, 0, len(found))
	for _, v := range found {
		p, err := vfs.ParsePath(v.Path)
		if err != nil {
			return nil, err
		}
		files = append(files, session.SeedFile{Path: v.Path, Read: func(ctx context.Context) ([]byte, error) {
			content, _, err := store.Read(ctx, p, v.Version)
			return content, err
		}})
	}
	return files, nil
}
