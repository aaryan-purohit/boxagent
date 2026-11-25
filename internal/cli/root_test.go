package cli

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/aaryan-purohit/boxagent/internal/config"
)

func noEnv(string) string { return "" }

func TestRootVersion(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	cmd := newRootCmd("v1.2.3", noEnv)
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--version"})

	err := cmd.Execute()

	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if got, want := out.String(), "boxagent version v1.2.3\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestRootRejectsUnknownCommand(t *testing.T) {
	t.Parallel()
	cmd := newRootCmd("dev", noEnv)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"nope"})

	if err := cmd.Execute(); err == nil {
		t.Error("Execute() = nil, want an error for an unknown command")
	}
}

func TestNewLoggerHonoursLevel(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := newLogger(&buf, config.Config{LogLevel: slog.LevelWarn})

	log.Info("quiet")
	log.Warn("loud")

	if out := buf.String(); strings.Contains(out, "quiet") || !strings.Contains(out, "loud") {
		t.Errorf("log output = %q, want only the warning", out)
	}
}
