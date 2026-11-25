package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrDuplicateTool means two tools share a name.
var ErrDuplicateTool = errors.New("duplicate tool")

// Tool is an action the model can take.
type Tool interface {
	Name() string
	Description() string
	Schema() map[string]any // JSON Schema for the arguments
	Call(ctx context.Context, args json.RawMessage) (any, error)
}

// ToolSpec declares a tool to the model.
type ToolSpec struct {
	Name        string
	Description string
	Schema      map[string]any
}

// Registry holds the tools available in a run.
type Registry struct {
	byName map[string]Tool
	specs  []ToolSpec
}

// NewRegistry registers tools, keeping their order.
func NewRegistry(tools ...Tool) (*Registry, error) {
	r := &Registry{byName: make(map[string]Tool, len(tools))}
	for _, t := range tools {
		name := t.Name()
		if _, ok := r.byName[name]; ok {
			return nil, fmt.Errorf("%w %q", ErrDuplicateTool, name)
		}
		r.byName[name] = t
		r.specs = append(r.specs, ToolSpec{Name: name, Description: t.Description(), Schema: t.Schema()})
	}
	return r, nil
}

// call runs the named tool. Every failure becomes an {"error": ...} result, so
// the model can see it and recover.
func (r *Registry) call(ctx context.Context, c ToolCall) ToolResult {
	content, err := r.invoke(ctx, c)
	if err != nil {
		content = errorContent(err)
	}
	return ToolResult{CallID: c.ID, Name: c.Name, Content: content}
}

func (r *Registry) invoke(ctx context.Context, c ToolCall) (json.RawMessage, error) {
	t, ok := r.byName[c.Name]
	if !ok {
		return nil, fmt.Errorf("unknown tool %q", c.Name)
	}
	out, err := t.Call(ctx, c.Args)
	if err != nil {
		return nil, err
	}
	return encodeTruncated(out)
}

func errorContent(err error) json.RawMessage {
	// Encoding a map of one string cannot fail.
	content, _ := encodeTruncated(map[string]string{"error": err.Error()})
	return content
}
