package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// maxOutputBytes is how much of one string is kept, not counting the marker.
const maxOutputBytes = 8 << 10

// encodeTruncated encodes v as JSON with every string value truncated, so
// each output field (such as stdout and stderr) is capped separately.
func encodeTruncated(v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode tool result: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // keep large integers exact
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, fmt.Errorf("decode tool result: %w", err)
	}
	return json.Marshal(truncateStrings(tree))
}

// truncateStrings truncates every string value in a decoded JSON tree.
func truncateStrings(v any) any {
	switch v := v.(type) {
	case string:
		return truncate(v, maxOutputBytes)
	case []any:
		for i := range v {
			v[i] = truncateStrings(v[i])
		}
	case map[string]any:
		for k := range v {
			v[k] = truncateStrings(v[k])
		}
	}
	return v
}

// truncate keeps at most limit bytes of s, split between its head and tail,
// with a marker between them. It never splits a rune.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	head := limit / 2
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	tail := len(s) - limit/2
	for tail < len(s) && !utf8.RuneStart(s[tail]) {
		tail++
	}
	return fmt.Sprintf("%s\n[truncated %d bytes]\n%s", s[:head], tail-head, s[tail:])
}
