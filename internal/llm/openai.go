// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

// OpenAIOptions configures the OpenAI-compatible adapter. It covers OpenAI
// itself and any Chat Completions compatible endpoint (DeepSeek, Qwen,
// Volcengine Ark, Ollama, vLLM, LiteLLM/Bifrost gateways...).
type OpenAIOptions struct {
	Name    string
	Model   string
	APIKey  string
	BaseURL string
}

// OpenAI implements Provider over the Chat Completions API.
type OpenAI struct {
	opts   OpenAIOptions
	client openai.Client
}

// NewOpenAI builds the adapter.
func NewOpenAI(o OpenAIOptions) *OpenAI {
	reqOpts := []option.RequestOption{option.WithAPIKey(o.APIKey)}
	if o.BaseURL != "" {
		reqOpts = append(reqOpts, option.WithBaseURL(o.BaseURL))
	}
	if o.Name == "" {
		o.Name = "openai"
	}
	return &OpenAI{opts: o, client: openai.NewClient(reqOpts...)}
}

// Name implements Provider.
func (p *OpenAI) Name() string { return p.opts.Name }

// Model implements Provider.
func (p *OpenAI) Model() string { return p.opts.Model }

// Chat implements Provider.
func (p *OpenAI) Chat(ctx context.Context, req *Request) (*Response, error) {
	var msgs []openai.ChatCompletionMessageParamUnion
	if req.System != "" {
		msgs = append(msgs, openai.SystemMessage(req.System))
	}
	for _, m := range req.Messages {
		msgs = append(msgs, toOpenAIMessages(m)...)
	}
	params := openai.ChatCompletionNewParams{
		Model:    p.opts.Model,
		Messages: msgs,
	}
	for _, t := range req.Tools {
		params.Tools = append(params.Tools, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        t.Name,
			Description: openai.String(t.Description),
			Parameters:  shared.FunctionParameters(jsonSchemaObject(t.Properties, t.Required)),
		}))
	}

	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		if isNotFound(err) {
			return nil, notFound(p.opts.Model, p.opts.Name, p.opts.BaseURL)
		}
		return nil, fmt.Errorf("%s: %w", p.opts.Name, err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("%s: empty response", p.opts.Name)
	}
	choice := resp.Choices[0]
	out := &Response{
		Message: Message{
			Role:   RoleAssistant,
			Text:   choice.Message.Content,
			Native: choice.Message.ToParam(),
		},
		// prompt_tokens includes cached tokens; split them so the fields do
		// not overlap (see Usage).
		Usage: Usage{
			InputTokens:     resp.Usage.PromptTokens - resp.Usage.PromptTokensDetails.CachedTokens,
			OutputTokens:    resp.Usage.CompletionTokens,
			CacheReadTokens: resp.Usage.PromptTokensDetails.CachedTokens,
		},
	}
	for _, tc := range choice.Message.ToolCalls {
		if tc.Type != "function" {
			continue
		}
		out.Message.ToolCalls = append(out.Message.ToolCalls, ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: json.RawMessage(tc.Function.Arguments),
		})
	}

	// Some compatible servers report "stop" even when returning tool calls.
	switch {
	case len(out.Message.ToolCalls) > 0:
		out.StopReason = StopToolUse
	case choice.FinishReason == "length":
		out.StopReason = StopMaxTokens
	case choice.FinishReason == "content_filter" || choice.Message.Refusal != "":
		out.StopReason = StopRefusal
		out.StopDetail = choice.Message.Refusal
	case choice.FinishReason == "stop" || choice.FinishReason == "":
		out.StopReason = StopEndTurn
	default:
		out.StopReason = StopOther
		out.StopDetail = choice.FinishReason
	}
	return out, nil
}

func toOpenAIMessages(m Message) []openai.ChatCompletionMessageParamUnion {
	if m.Role == RoleAssistant {
		if native, ok := m.Native.(openai.ChatCompletionMessageParamUnion); ok {
			return []openai.ChatCompletionMessageParamUnion{native}
		}
		asst := openai.ChatCompletionAssistantMessageParam{}
		if m.Text != "" {
			asst.Content.OfString = openai.String(m.Text)
		}
		for _, tc := range m.ToolCalls {
			asst.ToolCalls = append(asst.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
					ID: tc.ID,
					Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      tc.Name,
						Arguments: string(nonEmptyJSON(tc.Args)),
					},
				},
			})
		}
		return []openai.ChatCompletionMessageParamUnion{{OfAssistant: &asst}}
	}
	var out []openai.ChatCompletionMessageParamUnion
	for _, tr := range m.ToolResults {
		content := tr.Content
		if tr.IsError {
			content = "ERROR: " + content
		}
		out = append(out, openai.ToolMessage(content, tr.CallID))
	}
	if m.Text != "" {
		out = append(out, openai.UserMessage(m.Text))
	}
	return out
}

func jsonSchemaObject(props map[string]any, required []string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
