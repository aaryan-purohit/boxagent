package gemini

import (
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"google.golang.org/genai"
)

const (
	maxAttempts = 5
	baseDelay   = 2 * time.Second
	maxDelay    = 30 * time.Second
	// maxServerDelay is the longest wait a rate limit may ask for. Longer ones
	// are quotas that won't recover during a run.
	maxServerDelay = 90 * time.Second
)

// retryDelay returns how long to wait after a failed attempt (counted from 1),
// or false if err should not be retried.
func retryDelay(err error, attempt int, jitter float64) (time.Duration, bool) {
	if attempt >= maxAttempts || !retryable(err) {
		return 0, false
	}
	asked := serverDelay(err)
	if asked > maxServerDelay {
		return 0, false
	}
	return max(asked, backoff(attempt, jitter)), true
}

// retryable reports whether err is a rate limit, a server error, a network
// failure or an unusable response that another attempt may fix.
func retryable(err error) bool {
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code == http.StatusTooManyRequests || apiErr.Code >= http.StatusInternalServerError
	}
	var netErr net.Error
	return errors.As(err, &netErr) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, ErrMalformed) ||
		errors.Is(err, ErrNoContent)
}

// serverDelay returns the wait the server asked for, or 0.
func serverDelay(err error) time.Duration {
	var apiErr genai.APIError
	if !errors.As(err, &apiErr) {
		return 0
	}
	for _, detail := range apiErr.Details {
		if detail["@type"] != "type.googleapis.com/google.rpc.RetryInfo" {
			continue
		}
		text, _ := detail["retryDelay"].(string)
		if d, err := time.ParseDuration(text); err == nil {
			return d
		}
	}
	return 0
}

// backoff returns the delay after a failed attempt (counted from 1). It doubles
// each time up to maxDelay; jitter, in [0, 1), picks a point in its upper half.
func backoff(attempt int, jitter float64) time.Duration {
	d := baseDelay
	for i := 1; i < attempt && d < maxDelay; i++ {
		d *= 2
	}
	d = min(d, maxDelay)
	return d/2 + time.Duration(jitter*float64(d/2))
}
