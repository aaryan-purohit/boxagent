// Package tools implements the actions the model can take.
package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrInvalidArgs means the model's arguments are unusable.
var ErrInvalidArgs = errors.New("invalid arguments")

// decode parses args strictly, so the model learns about misspelt fields.
// Missing args are treated as an empty object.
func decode[T any](args json.RawMessage) (T, error) {
	var v T
	if len(bytes.TrimSpace(args)) == 0 {
		return v, nil
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, fmt.Errorf("%w: %w", ErrInvalidArgs, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return v, fmt.Errorf("%w: unexpected data after the JSON object", ErrInvalidArgs)
	}
	return v, nil
}

func missing(field string) error {
	return fmt.Errorf("%w: %s is required", ErrInvalidArgs, field)
}

// object builds the JSON Schema of an arguments object.
func object(properties map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func property(kind, description string) map[string]any {
	return map[string]any{"type": kind, "description": description}
}
