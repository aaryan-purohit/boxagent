// Package gemini implements agent.Model on the Google Gemini API.
package gemini

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"

	"google.golang.org/genai"

	"github.com/aaryan-purohit/boxagent/internal/agent"
)

// requestTimeout bounds one HTTP attempt, so a stalled connection can't hang a
// run.
const requestTimeout = 5 * time.Minute

// baseURL is set explicitly because the SDK would otherwise take it from the
// environment, which could send the API key elsewhere.
const baseURL = "https://generativelanguage.googleapis.com/"

var errNoAPIKey = errors.New("gemini: empty API key")

// Options configures a Model.
type Options struct {
	APIKey string
	Model  string
	Log    *slog.Logger
}

// Model is an agent.Model backed by Gemini.
type Model struct {
	client *genai.Client
	name   string
	log    *slog.Logger
	sleep  func(context.Context, time.Duration) error
}

// New returns a Model.
func New(ctx context.Context, opts Options) (*Model, error) {
	return newModel(ctx, opts, &http.Client{Timeout: requestTimeout})
}

func newModel(ctx context.Context, opts Options, hc *http.Client) (*Model, error) {
	if opts.APIKey == "" {
		return nil, errNoAPIKey // the SDK would silently look in the environment
	}
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:      opts.APIKey,
		Backend:     genai.BackendGeminiAPI,
		HTTPClient:  hc,
		HTTPOptions: genai.HTTPOptions{BaseURL: baseURL},
	})
	if err != nil {
		return nil, fmt.Errorf("gemini client: %w", err)
	}
	return &Model{client: client, name: opts.Model, log: opts.Log, sleep: sleep}, nil
}

// Generate returns the model's next message, retrying failures that are likely
// to pass.
func (m *Model) Generate(ctx context.Context, req agent.Request) (agent.Message, error) {
	contents, err := toContents(req.Messages)
	if err != nil {
		return agent.Message{}, fmt.Errorf("gemini: %w", err)
	}
	cfg := toConfig(req)
	// The history length makes IDs unique across the turns of a run.
	idPrefix := fmt.Sprintf("call-%d", len(req.Messages))
	for attempt := 1; ; attempt++ {
		msg, err := m.generate(ctx, contents, cfg, idPrefix)
		if err == nil {
			return msg, nil
		}
		if ctx.Err() != nil {
			return agent.Message{}, ctx.Err()
		}
		delay, ok := retryDelay(err, attempt, rand.Float64())
		if !ok {
			return agent.Message{}, fmt.Errorf("gemini %s: attempt %d of %d: %w", m.name, attempt, maxAttempts, err)
		}
		m.log.Warn("retrying model request", "attempt", attempt, "delay", delay.Round(time.Millisecond), "error", err)
		if err := m.sleep(ctx, delay); err != nil {
			return agent.Message{}, err
		}
	}
}

// generate makes one attempt.
func (m *Model) generate(ctx context.Context, contents []*genai.Content, cfg *genai.GenerateContentConfig, idPrefix string) (agent.Message, error) {
	resp, err := m.client.Models.GenerateContent(ctx, m.name, contents, cfg)
	if err != nil {
		return agent.Message{}, err
	}
	return toMessage(resp, idPrefix)
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
