// Package config turns environment variables into a validated Config.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
)

const (
	defaultModel    = "gemini-3.5-flash-lite"
	defaultDataDir  = ".boxagent" // relative to $HOME
	defaultImage    = "golang:1.23-bookworm"
	defaultMaxTurns = 25
)

var (
	// ErrMissingAPIKey means GEMINI_API_KEY is unset.
	ErrMissingAPIKey = errors.New("GEMINI_API_KEY is not set")
	// ErrInvalid means a variable is set to an unusable value.
	ErrInvalid = errors.New("invalid configuration")
)

// Config holds validated settings.
type Config struct {
	APIKey   string
	Model    string
	DataDir  string
	Image    string
	MaxTurns int
	LogLevel slog.Level
}

// Load reads every setting through getenv and validates it. The API key may be
// empty, because only some commands need it; see RequireAPIKey.
func Load(getenv func(string) string) (Config, error) {
	dataDir, err := parseDataDir(getenv("BOXAGENT_DATA_DIR"), getenv("HOME"))
	if err != nil {
		return Config{}, err
	}
	maxTurns, err := parseMaxTurns(getenv("BOXAGENT_MAX_TURNS"))
	if err != nil {
		return Config{}, err
	}
	level, err := parseLogLevel(getenv("BOXAGENT_LOG_LEVEL"))
	if err != nil {
		return Config{}, err
	}
	return Config{
		APIKey:   getenv("GEMINI_API_KEY"),
		Model:    orDefault(getenv("BOXAGENT_MODEL"), defaultModel),
		DataDir:  dataDir,
		Image:    orDefault(getenv("BOXAGENT_IMAGE"), defaultImage),
		MaxTurns: maxTurns,
		LogLevel: level,
	}, nil
}

// RequireAPIKey returns ErrMissingAPIKey if no API key is configured.
func (c Config) RequireAPIKey() error {
	if c.APIKey == "" {
		return ErrMissingAPIKey
	}
	return nil
}

// LogValue keeps the API key out of logs.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", c.Model),
		slog.String("data_dir", c.DataDir),
		slog.String("image", c.Image),
		slog.Int("max_turns", c.MaxTurns),
		slog.String("log_level", c.LogLevel.String()),
	)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func parseDataDir(dir, home string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	if home == "" {
		return "", fmt.Errorf("%w: set BOXAGENT_DATA_DIR or HOME", ErrInvalid)
	}
	return filepath.Join(home, defaultDataDir), nil
}

func parseMaxTurns(s string) (int, error) {
	if s == "" {
		return defaultMaxTurns, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%w: BOXAGENT_MAX_TURNS %q: want a positive integer", ErrInvalid, s)
	}
	return n, nil
}

func parseLogLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("%w: BOXAGENT_LOG_LEVEL %q: want debug, info, warn or error", ErrInvalid, s)
}
