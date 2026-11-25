// Package vfs is a versioned, content-addressed file store.
package vfs

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

const maxPathBytes = 512

// ErrInvalidPath means a path failed validation.
var ErrInvalidPath = errors.New("invalid path")

// Path is a validated, clean, relative file path. Build it with ParsePath.
type Path struct{ s string }

// String returns the slash-separated path.
func (p Path) String() string { return p.s }

// ParsePath validates raw and returns it cleaned. Checks run on the raw string,
// so "a/../b" is rejected rather than normalised.
func ParsePath(raw string) (Path, error) {
	if reason := rejectReason(raw); reason != "" {
		return Path{}, fmt.Errorf("%w %q: %s", ErrInvalidPath, raw, reason)
	}
	return Path{path.Clean(raw)}, nil
}

func rejectReason(raw string) string {
	switch {
	case raw == "":
		return "empty"
	case len(raw) > maxPathBytes:
		return fmt.Sprintf("longer than %d bytes", maxPathBytes)
	case strings.ContainsRune(raw, 0):
		return "contains a NUL byte"
	case strings.Contains(raw, `\`):
		return "contains a backslash"
	case path.IsAbs(raw):
		return "absolute"
	case slices.Contains(strings.Split(raw, "/"), ".."):
		return "contains a .. segment"
	case path.Clean(raw) == ".":
		return "empty"
	}
	return ""
}

// Prefix selects a file or everything under a directory. The zero value
// selects every file.
type Prefix struct{ s string }

// ParsePrefix validates raw by the same rules as ParsePath, except that an
// empty string selects every file.
func ParsePrefix(raw string) (Prefix, error) {
	if raw == "" {
		return Prefix{}, nil
	}
	p, err := ParsePath(raw)
	return Prefix(p), err
}
