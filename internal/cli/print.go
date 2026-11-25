package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/aaryan-purohit/boxagent/internal/agent"
)

// Limits, in characters, on what one step prints.
const (
	maxPrintedArgs   = 200
	maxPrintedResult = 500
)

// printEvent writes one step of a run to w.
func printEvent(w io.Writer, e agent.Event) {
	var line string
	switch e := e.(type) {
	case agent.TextEvent:
		line = strings.TrimSpace(e.Text)
	case agent.CallEvent:
		line = "> " + e.Call.Name + " " + shorten(readable(e.Call.Args), maxPrintedArgs)
	case agent.ResultEvent:
		line = "< " + shorten(readable(e.Result.Content), maxPrintedResult)
	}
	// A broken stdout raises SIGPIPE, which cancels the run.
	_, _ = fmt.Fprintln(w, stripControl(line))
}

// readable re-encodes JSON without HTML escapes such as > for >.
func readable(raw json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return string(raw)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return string(raw)
	}
	return strings.TrimSpace(buf.String())
}

func shorten(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "..."
}

// stripControl removes characters that could manipulate the user's terminal,
// because the text comes from the model and from commands it ran.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return -1
		}
		return r
	}, s)
}
