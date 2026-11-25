package vfs

import (
	"errors"
	"strings"
	"testing"
)

func TestParsePath(t *testing.T) {
	t.Parallel()
	longest := strings.Repeat("a", 512)
	tests := []struct{ name, raw, want string }{
		{"accepts a file name", "main.go", "main.go"},
		{"accepts nested directories", "a/b/c.txt", "a/b/c.txt"},
		{"accepts a hidden file", ".gitignore", ".gitignore"},
		{"accepts dots inside a name", "a..b/...", "a..b/..."},
		{"accepts spaces and unicode", "my dir/é.txt", "my dir/é.txt"},
		{"accepts the longest path", longest, longest},
		{"cleans a leading dot segment", "./a", "a"},
		{"cleans an inner dot segment", "a/./b", "a/b"},
		{"cleans repeated slashes", "a//b", "a/b"},
		{"cleans a trailing slash", "a/", "a"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParsePath(tc.raw)

			if err != nil || got.String() != tc.want {
				t.Errorf("ParsePath(%q) = %q, %v, want %q", tc.raw, got, err, tc.want)
			}
		})
	}
}

func TestParsePathRejects(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, raw string }{
		{"empty", ""},
		{"only a dot", "."},
		{"only dot segments", "././"},
		{"absolute", "/etc/passwd"},
		{"double-slash absolute", "//etc"},
		{"bare parent", ".."},
		{"leading parent traversal", "../a"},
		{"inner parent that stays inside", "a/../b"},
		{"trailing parent", "a/.."},
		{"parent after dot segment", "./../a"},
		{"NUL byte", "a\x00b"},
		{"backslash", `a\b`},
		{"backslash traversal", `..\a`},
		{"longer than 512 bytes", strings.Repeat("a", 513)},
		{"longer than 512 bytes before cleaning", strings.Repeat("./", 256) + "a"},
		{"longer than 512 bytes in multi-byte runes", strings.Repeat("é", 257)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ParsePath(tc.raw); !errors.Is(err, ErrInvalidPath) {
				t.Errorf("ParsePath(%q) error = %v, want ErrInvalidPath", tc.raw, err)
			}
			if tc.raw == "" {
				return // an empty prefix is valid
			}
			if _, err := ParsePrefix(tc.raw); !errors.Is(err, ErrInvalidPath) {
				t.Errorf("ParsePrefix(%q) error = %v, want ErrInvalidPath", tc.raw, err)
			}
		})
	}
}
