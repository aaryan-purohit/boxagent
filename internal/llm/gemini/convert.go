package gemini

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/genai"

	"github.com/aaryan-purohit/boxagent/internal/agent"
)

var (
	// ErrBlocked means Gemini refused to answer the prompt.
	ErrBlocked = errors.New("prompt blocked")
	// ErrNoContent means the response held no text and no tool calls.
	ErrNoContent = errors.New("response has no content")
	// ErrStopped means generation ended abnormally, for example at the token
	// limit.
	ErrStopped = errors.New("generation stopped early")
	// ErrMalformed means the model produced an unusable tool call or response.
	ErrMalformed = errors.New("malformed response")
)

func toConfig(req agent.Request) *genai.GenerateContentConfig {
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: req.System}}},
	}
	if len(req.Tools) == 0 {
		return cfg
	}
	tool := &genai.Tool{}
	for _, s := range req.Tools {
		tool.FunctionDeclarations = append(tool.FunctionDeclarations, &genai.FunctionDeclaration{
			Name:                 s.Name,
			Description:          s.Description,
			ParametersJsonSchema: s.Schema,
		})
	}
	cfg.Tools = []*genai.Tool{tool}
	return cfg
}

func toContents(msgs []agent.Message) ([]*genai.Content, error) {
	contents := make([]*genai.Content, 0, len(msgs))
	for _, m := range msgs {
		c, err := toContent(m)
		if err != nil {
			return nil, err
		}
		contents = append(contents, c)
	}
	return contents, nil
}

func toContent(m agent.Message) (*genai.Content, error) {
	c := &genai.Content{Role: genai.RoleUser}
	if m.Role == agent.RoleAssistant {
		c.Role = genai.RoleModel
	}
	if m.Text != "" || len(m.Signature) > 0 {
		c.Parts = append(c.Parts, &genai.Part{Text: m.Text, ThoughtSignature: m.Signature})
	}
	for _, call := range m.ToolCalls {
		var args map[string]any
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return nil, fmt.Errorf("encode %s call: %w", call.Name, err)
		}
		c.Parts = append(c.Parts, &genai.Part{
			FunctionCall:     &genai.FunctionCall{ID: call.ID, Name: call.Name, Args: args},
			ThoughtSignature: call.Signature,
		})
	}
	for _, r := range m.ToolResults {
		response, err := toResponse(r.Content)
		if err != nil {
			return nil, fmt.Errorf("encode %s result: %w", r.Name, err)
		}
		c.Parts = append(c.Parts, &genai.Part{
			FunctionResponse: &genai.FunctionResponse{ID: r.CallID, Name: r.Name, Response: response},
		})
	}
	return c, nil
}

// toResponse decodes a tool result. Gemini accepts only objects, so anything
// else is wrapped under its conventional "output" key.
func toResponse(content json.RawMessage) (map[string]any, error) {
	var v any
	if err := json.Unmarshal(content, &v); err != nil {
		return nil, err
	}
	if obj, ok := v.(map[string]any); ok {
		return obj, nil
	}
	return map[string]any{"output": v}, nil
}

// toMessage converts a response. Calls without an ID get one starting with
// idPrefix.
func toMessage(resp *genai.GenerateContentResponse, idPrefix string) (agent.Message, error) {
	if f := resp.PromptFeedback; f != nil && f.BlockReason != "" {
		return agent.Message{}, fmt.Errorf("%w: %s %s", ErrBlocked, f.BlockReason, f.BlockReasonMessage)
	}
	if len(resp.Candidates) == 0 {
		return agent.Message{}, ErrNoContent
	}
	c := resp.Candidates[0]
	switch c.FinishReason {
	case "", genai.FinishReasonUnspecified, genai.FinishReasonStop:
	case genai.FinishReasonMalformedFunctionCall, genai.FinishReasonMalformedResponse, genai.FinishReasonUnexpectedToolCall:
		return agent.Message{}, fmt.Errorf("%w: %s %s", ErrMalformed, c.FinishReason, c.FinishMessage)
	default:
		return agent.Message{}, fmt.Errorf("%w: %s %s", ErrStopped, c.FinishReason, c.FinishMessage)
	}
	if c.Content == nil {
		return agent.Message{}, ErrNoContent
	}
	msg := agent.Message{Role: agent.RoleAssistant}
	for _, p := range c.Content.Parts {
		if err := addPart(&msg, p, idPrefix); err != nil {
			return agent.Message{}, err
		}
	}
	if strings.TrimSpace(msg.Text) == "" && len(msg.ToolCalls) == 0 {
		return agent.Message{}, ErrNoContent
	}
	return msg, nil
}

func addPart(msg *agent.Message, p *genai.Part, idPrefix string) error {
	if p.Thought {
		return nil // a summary for display; the signature carries the state
	}
	if p.FunctionCall == nil {
		msg.Text += p.Text
		if len(p.ThoughtSignature) > 0 {
			msg.Signature = p.ThoughtSignature
		}
		return nil
	}
	args := json.RawMessage(`{}`)
	if len(p.FunctionCall.Args) > 0 {
		var err error
		if args, err = json.Marshal(p.FunctionCall.Args); err != nil {
			return fmt.Errorf("decode %s call: %w", p.FunctionCall.Name, err)
		}
	}
	id := p.FunctionCall.ID
	if id == "" {
		id = fmt.Sprintf("%s-%d", idPrefix, len(msg.ToolCalls))
	}
	msg.ToolCalls = append(msg.ToolCalls, agent.ToolCall{
		ID: id, Name: p.FunctionCall.Name, Args: args, Signature: p.ThoughtSignature,
	})
	return nil
}
