package agent

import (
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
)

func TestTruncate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in string
		limit    int
		want     string
	}{
		{"keeps an empty string", "", 4, ""},
		{"keeps a short string", "abc", 4, "abc"},
		{"keeps a string at the limit", "abcd", 4, "abcd"},
		{"cuts one byte over the limit", "abcde", 4, "ab\n[truncated 1 bytes]\nde"},
		{"keeps head and tail", "abcdefghij", 4, "ab\n[truncated 6 bytes]\nij"},
		{"rounds an odd limit down", "abcdefghij", 5, "ab\n[truncated 6 bytes]\nij"},
		{"drops a rune split by the head", "aébcdef", 4, "a\n[truncated 5 bytes]\nef"},
		{"drops a rune split by the tail", "abcdeéf", 4, "ab\n[truncated 5 bytes]\nf"},
		{"drops everything when no rune fits", "世界", 2, "\n[truncated 6 bytes]\n"},
		{"keeps whole multi-byte runes", "世xxxx界", 6, "世\n[truncated 4 bytes]\n界"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := truncate(tc.in, tc.limit)

			if got != tc.want || !utf8.ValidString(got) {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.limit, got, tc.want)
			}
		})
	}
}

func TestEncodeTruncatedPreservesOtherValues(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"big": int64(9007199254740993), "f": 1.5, "t": true, "n": nil,
		"nested": []any{map[string]any{"s": "x"}}, "bad": "a\xffb",
	}

	got, err := encodeTruncated(in)

	if err != nil {
		t.Fatalf("encodeTruncated() error: %v", err)
	}
	want := `{"bad":"a` + "�" + `b","big":9007199254740993,"f":1.5,"n":null,"nested":[{"s":"x"}],"t":true}`
	if diff := cmp.Diff(want, string(got)); diff != "" {
		t.Errorf("encodeTruncated() mismatch (-want +got):\n%s", diff)
	}
}
