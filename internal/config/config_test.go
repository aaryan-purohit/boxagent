package config

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func fakeEnv(env map[string]string) func(string) string {
	return func(key string) string { return env[key] }
}

func TestLoad(t *testing.T) {
	t.Parallel()
	defaults := Config{
		Model:    "gemini-3.5-flash-lite",
		DataDir:  "/home/u/.boxagent",
		Image:    "golang:1.23-bookworm",
		MaxTurns: 25,
		LogLevel: slog.LevelInfo,
	}
	withLevel := func(l slog.Level) Config {
		c := defaults
		c.LogLevel = l
		return c
	}
	tests := []struct {
		name string
		env  map[string]string
		want Config
	}{
		{"uses defaults and allows a missing key", nil, defaults},
		{
			"overrides every setting",
			map[string]string{
				"GEMINI_API_KEY":     "k",
				"BOXAGENT_MODEL":     "m",
				"BOXAGENT_DATA_DIR":  "/data",
				"BOXAGENT_IMAGE":     "img",
				"BOXAGENT_MAX_TURNS": "1",
				"BOXAGENT_LOG_LEVEL": "error",
			},
			Config{APIKey: "k", Model: "m", DataDir: "/data", Image: "img", MaxTurns: 1, LogLevel: slog.LevelError},
		},
		{"accepts debug level", map[string]string{"BOXAGENT_LOG_LEVEL": "debug"}, withLevel(slog.LevelDebug)},
		{"accepts info level", map[string]string{"BOXAGENT_LOG_LEVEL": "info"}, withLevel(slog.LevelInfo)},
		{"accepts warn level", map[string]string{"BOXAGENT_LOG_LEVEL": "warn"}, withLevel(slog.LevelWarn)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{"HOME": "/home/u"}
			for k, v := range tc.env {
				env[k] = v
			}

			got, err := Load(fakeEnv(env))

			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Load() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLoadRejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"non-numeric max turns", map[string]string{"HOME": "/h", "BOXAGENT_MAX_TURNS": "many"}},
		{"zero max turns", map[string]string{"HOME": "/h", "BOXAGENT_MAX_TURNS": "0"}},
		{"negative max turns", map[string]string{"HOME": "/h", "BOXAGENT_MAX_TURNS": "-1"}},
		{"unknown log level", map[string]string{"HOME": "/h", "BOXAGENT_LOG_LEVEL": "loud"}},
		{"no home and no data dir", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := Load(fakeEnv(tc.env))

			if !errors.Is(err, ErrInvalid) {
				t.Errorf("Load() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestRequireAPIKey(t *testing.T) {
	t.Parallel()
	if err := (Config{}).RequireAPIKey(); !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("RequireAPIKey() without key = %v, want ErrMissingAPIKey", err)
	}
	if err := (Config{APIKey: "k"}).RequireAPIKey(); err != nil {
		t.Errorf("RequireAPIKey() with key = %v, want nil", err)
	}
}

func TestConfigLogOmitsAPIKey(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer

	slog.New(slog.NewTextHandler(&buf, nil)).Info("config", "cfg", Config{APIKey: "sekrit", Model: "m"})

	if out := buf.String(); strings.Contains(out, "sekrit") || !strings.Contains(out, "cfg.model=m") {
		t.Errorf("log output = %q, want the model and no API key", out)
	}
}
