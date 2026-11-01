// SPDX-License-Identifier: Apache-2.0

// Package llm is a thin, provider-neutral chat + tool-calling interface.
//
// It deliberately covers only what the investigation loop needs. Provider
// adapters keep each assistant turn in its native wire form (Message.Native)
// and replay it verbatim, so provider-specific content such as reasoning
// blocks survives across turns: conversation history is append-only.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

// Role of a message author.
type Role string

// Roles.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one conversation turn.
type Message struct {
	Role Role
	// Text is the plain-text content (user prompt or assistant prose).
	Text string
	// ToolCalls are requested by the assistant.
	ToolCalls []ToolCall
	// ToolResults answer the previous assistant turn's ToolCalls (user role).
	ToolResults []ToolResult
	// Thinking is the readable reasoning summary the provider returned, if any.
	// It is recorded for review only; Native carries the provider's own
	// reasoning blocks when the turn is sent back.
	Thinking string
	// Native holds the provider's own representation of an assistant turn.
	// Adapters prefer it over Text/ToolCalls when rebuilding history.
	Native any
}

// ToolCall is a function invocation requested by the model.
type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}

// ToolResult is the output of a ToolCall.
type ToolResult struct {
	CallID  string
	Content string
	IsError bool
}

// ToolSpec describes a callable tool.
type ToolSpec struct {
	Name        string
	Description string
	Properties  map[string]any
	Required    []string
}

// Request is a single model call.
type Request struct {
	System    string
	Messages  []Message
	Tools     []ToolSpec
	MaxTokens int
}

// StopReason explains why the model stopped.
type StopReason string

// Stop reasons.
const (
	StopEndTurn   StopReason = "end_turn"
	StopToolUse   StopReason = "tool_use"
	StopMaxTokens StopReason = "max_tokens"
	StopRefusal   StopReason = "refusal"
	StopOther     StopReason = "other"
)

// Usage counts tokens for one call. The three input fields do not overlap:
// InputTokens is input that was neither read from nor written to the prompt
// cache, so TotalInput is their sum. Adapters normalize to this.
type Usage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// TotalInput is every input token the model processed, cached or not.
func (u Usage) TotalInput() int64 {
	return u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

// Add accumulates u2 into u.
func (u *Usage) Add(u2 Usage) {
	u.InputTokens += u2.InputTokens
	u.OutputTokens += u2.OutputTokens
	u.CacheReadTokens += u2.CacheReadTokens
	u.CacheWriteTokens += u2.CacheWriteTokens
}

// Response is the model's reply.
type Response struct {
	Message    Message
	StopReason StopReason
	// StopDetail carries provider detail for refusals or unusual stops.
	StopDetail string
	Usage      Usage
}

// Provider is a chat model with tool calling.
type Provider interface {
	// Name identifies the provider preset, e.g. "anthropic" or "deepseek".
	Name() string
	Model() string
	Chat(ctx context.Context, req *Request) (*Response, error)
}

// ErrRefused is returned by callers that treat a refusal as terminal.
var ErrRefused = fmt.Errorf("model declined the request")
