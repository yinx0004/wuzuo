// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicOptions configures the Claude adapter.
type AnthropicOptions struct {
	Name    string
	Model   string
	APIKey  string // empty: use the SDK's credential chain
	BaseURL string
	// Effort is passed as output_config.effort when non-empty.
	Effort string
	// RefusalFallback enables server-side fallback for declined requests.
	// Set it only for models that support it, on the first-party API.
	RefusalFallback bool
}

// Anthropic talks to Claude through the official SDK.
type Anthropic struct {
	opts   AnthropicOptions
	client anthropic.Client
}

// NewAnthropic builds the adapter.
func NewAnthropic(o AnthropicOptions) *Anthropic {
	var reqOpts []option.RequestOption
	if o.APIKey != "" {
		reqOpts = append(reqOpts, option.WithAPIKey(o.APIKey))
	}
	if o.BaseURL != "" {
		reqOpts = append(reqOpts, option.WithBaseURL(o.BaseURL))
	}
	// Which models support it is decided by config (the built-in model table
	// or llm.refusal_fallback), not here.
	if o.RefusalFallback {
		reqOpts = append(reqOpts,
			option.WithHeaderAdd("anthropic-beta", "server-side-fallback-2026-07-01"),
			option.WithJSONSet("fallbacks", "default"),
		)
	}
	if o.Name == "" {
		o.Name = "anthropic"
	}
	return &Anthropic{opts: o, client: anthropic.NewClient(reqOpts...)}
}

// Name implements Provider.
func (a *Anthropic) Name() string { return a.opts.Name }

// Model implements Provider.
func (a *Anthropic) Model() string { return a.opts.Model }

// Chat implements Provider.
func (a *Anthropic) Chat(ctx context.Context, req *Request) (*Response, error) {
	msgs, err := toAnthropicMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 16000
	}
	params := anthropic.MessageNewParams{
		Model:     a.opts.Model,
		MaxTokens: int64(maxTokens),
		Messages:  msgs,
		// Cache tools + system + the growing transcript: investigations resend
		// the whole history every turn.
		CacheControl: anthropic.NewCacheControlEphemeralParam(),
	}
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}
	for _, t := range req.Tools {
		tp := anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.ToolInputSchemaParam{Properties: t.Properties, Required: t.Required},
		}
		params.Tools = append(params.Tools, anthropic.ToolUnionParam{OfTool: &tp})
	}
	if a.opts.Effort != "" {
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(a.opts.Effort)}
	}

	resp, err := a.client.Messages.New(ctx, params)
	if err != nil {
		if isNotFound(err) {
			return nil, notFound(a.opts.Model, a.opts.Name, a.opts.BaseURL)
		}
		return nil, fmt.Errorf("anthropic: %w", err)
	}

	out := &Response{
		Message: Message{Role: RoleAssistant, Native: resp.ToParam()},
		Usage: Usage{
			InputTokens:      resp.Usage.InputTokens,
			OutputTokens:     resp.Usage.OutputTokens,
			CacheReadTokens:  resp.Usage.CacheReadInputTokens,
			CacheWriteTokens: resp.Usage.CacheCreationInputTokens,
		},
	}
	var text []string
	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			text = append(text, b.Text)
		case anthropic.ToolUseBlock:
			out.Message.ToolCalls = append(out.Message.ToolCalls, ToolCall{
				ID:   b.ID,
				Name: b.Name,
				Args: json.RawMessage(b.JSON.Input.Raw()),
			})
		}
	}
	out.Message.Text = strings.Join(text, "\n")

	switch resp.StopReason {
	case anthropic.StopReasonEndTurn:
		out.StopReason = StopEndTurn
	case anthropic.StopReasonToolUse:
		out.StopReason = StopToolUse
	case anthropic.StopReasonMaxTokens:
		out.StopReason = StopMaxTokens
	case anthropic.StopReasonRefusal:
		out.StopReason = StopRefusal
		out.StopDetail = strings.TrimSpace(fmt.Sprintf("%s %s", resp.StopDetails.Category, resp.StopDetails.Explanation))
	default:
		out.StopReason = StopOther
		out.StopDetail = string(resp.StopReason)
	}
	return out, nil
}

func toAnthropicMessages(in []Message) ([]anthropic.MessageParam, error) {
	out := make([]anthropic.MessageParam, 0, len(in))
	for _, m := range in {
		if m.Role == RoleAssistant {
			if native, ok := m.Native.(anthropic.MessageParam); ok {
				out = append(out, native)
				continue
			}
			var blocks []anthropic.ContentBlockParamUnion
			if m.Text != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Text))
			}
			for _, tc := range m.ToolCalls {
				var input any
				if err := json.Unmarshal(nonEmptyJSON(tc.Args), &input); err != nil {
					return nil, fmt.Errorf("tool call %s: %w", tc.ID, err)
				}
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, input, tc.Name))
			}
			out = append(out, anthropic.NewAssistantMessage(blocks...))
			continue
		}
		var blocks []anthropic.ContentBlockParamUnion
		for _, tr := range m.ToolResults {
			blocks = append(blocks, anthropic.NewToolResultBlock(tr.CallID, tr.Content, tr.IsError))
		}
		if m.Text != "" {
			blocks = append(blocks, anthropic.NewTextBlock(m.Text))
		}
		out = append(out, anthropic.NewUserMessage(blocks...))
	}
	return out, nil
}

func nonEmptyJSON(b json.RawMessage) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("{}")
	}
	return b
}
