// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// capture serves one canned JSON response and records the request.
func capture(t *testing.T, response string) (*httptest.Server, *map[string]any, *http.Header) {
	t.Helper()
	body := map[string]any{}
	hdr := http.Header{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("request is not JSON: %v", err)
		}
		for k, v := range r.Header {
			hdr[k] = v
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)
	return srv, &body, &hdr
}

var testReq = &Request{
	System: "sys",
	Messages: []Message{
		{Role: RoleUser, Text: "why slow?"},
		{Role: RoleAssistant, Text: "checking", ToolCalls: []ToolCall{{ID: "c1", Name: "probe", Args: json.RawMessage(`{"target":"db"}`)}}},
		{Role: RoleUser, ToolResults: []ToolResult{{CallID: "c1", Content: "E1 data"}}, Text: "continue"},
	},
	Tools: []ToolSpec{{Name: "probe", Description: "d", Properties: map[string]any{"target": map[string]any{"type": "string"}}, Required: []string{"target"}}},
}

func TestAnthropicWire(t *testing.T) {
	srv, body, hdr := capture(t, `{
		"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5",
		"content":[{"type":"text","text":"Looking at locks."},{"type":"tool_use","id":"tu_1","name":"probe","input":{"target":"db"}}],
		"stop_reason":"tool_use","stop_sequence":null,
		"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":80,"cache_creation_input_tokens":0}}`)
	p := NewAnthropic(AnthropicOptions{Model: "claude-opus-5-5", APIKey: "k", BaseURL: srv.URL, Effort: "high", RefusalFallback: true})
	resp, err := p.Chat(context.Background(), testReq)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != StopToolUse || len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].ID != "tu_1" {
		t.Fatalf("resp = %+v", resp)
	}
	if string(resp.Message.ToolCalls[0].Args) != `{"target":"db"}` || resp.Usage.CacheReadTokens != 80 {
		t.Fatalf("args/usage = %s %+v", resp.Message.ToolCalls[0].Args, resp.Usage)
	}
	if resp.Message.Native == nil {
		t.Fatal("native message not kept for replay")
	}
	b := *body
	if b["fallbacks"] != "default" || hdr.Get("Anthropic-Beta") != "server-side-fallback-2026-07-01" {
		t.Errorf("fallback not requested: %v / %q", b["fallbacks"], hdr.Get("Anthropic-Beta"))
	}
	if oc, _ := b["output_config"].(map[string]any); oc["effort"] != "high" {
		t.Errorf("effort = %v", b["output_config"])
	}
	if _, ok := b["cache_control"]; !ok {
		t.Error("top-level cache_control missing")
	}
	msgs := b["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %v", msgs)
	}
	user := msgs[2].(map[string]any)["content"].([]any)
	if user[0].(map[string]any)["type"] != "tool_result" || user[1].(map[string]any)["type"] != "text" {
		t.Errorf("tool_result must come first in the user turn: %v", user)
	}

	// Replaying the native message must reproduce the assistant turn verbatim.
	next := &Request{Messages: []Message{testReq.Messages[0], resp.Message}}
	if _, err := p.Chat(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	asst := (*body)["messages"].([]any)[1].(map[string]any)["content"].([]any)
	if asst[1].(map[string]any)["id"] != "tu_1" {
		t.Errorf("native replay lost tool_use: %v", asst)
	}
}

func TestAnthropicRefusal(t *testing.T) {
	srv, _, _ := capture(t, `{"id":"m","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],
		"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber","explanation":"x"},
		"usage":{"input_tokens":1,"output_tokens":0}}`)
	p := NewAnthropic(AnthropicOptions{Model: "claude-opus-5-5", APIKey: "k", BaseURL: srv.URL})
	resp, err := p.Chat(context.Background(), &Request{Messages: []Message{{Role: RoleUser, Text: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != StopRefusal || resp.StopDetail == "" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestOpenAIWire(t *testing.T) {
	srv, body, _ := capture(t, `{"id":"c","object":"chat.completion","created":1,"model":"deepseek-chat",
		"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":null,
		"tool_calls":[{"id":"call_1","type":"function","function":{"name":"probe","arguments":"{\"target\":\"db\"}"}}]}}],
		"usage":{"prompt_tokens":50,"completion_tokens":10,"total_tokens":60}}`)
	p := NewOpenAI(OpenAIOptions{Name: "deepseek", Model: "deepseek-chat", APIKey: "k", BaseURL: srv.URL})
	resp, err := p.Chat(context.Background(), testReq)
	if err != nil {
		t.Fatal(err)
	}
	// finish_reason "stop" with tool calls must still be treated as tool use.
	if resp.StopReason != StopToolUse || resp.Message.ToolCalls[0].Name != "probe" || resp.Usage.InputTokens != 50 {
		t.Fatalf("resp = %+v", resp)
	}
	msgs := (*body)["messages"].([]any)
	roles := []string{}
	for _, m := range msgs {
		roles = append(roles, m.(map[string]any)["role"].(string))
	}
	want := []string{"system", "user", "assistant", "tool", "user"}
	if len(roles) != len(want) {
		t.Fatalf("roles = %v", roles)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("roles = %v, want %v", roles, want)
		}
	}
	tools := (*body)["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["parameters"].(map[string]any)["type"] != "object" {
		t.Errorf("parameters = %v", fn["parameters"])
	}
}

func serve(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAnthropicListModels(t *testing.T) {
	srv := serve(t, 200, `{"data":[
		{"type":"model","id":"claude-sonnet-5-5","display_name":"Claude Sonnet 5.5","created_at":"2026-09-10T00:00:00Z"},
		{"type":"model","id":"claude-opus-6","display_name":"Claude Opus 6","created_at":"2026-12-01T00:00:00Z"}],
		"has_more":false,"first_id":"claude-sonnet-5-5","last_id":"claude-opus-6"}`)
	ms, err := NewAnthropic(AnthropicOptions{Model: "x", APIKey: "k", BaseURL: srv.URL}).ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].ID != "claude-opus-6" || ms[0].Created.Year() != 2026 || ms[1].Name != "Claude Sonnet 5.5" {
		t.Fatalf("models (want newest first): %+v", ms)
	}
}

func TestOpenAIListModels(t *testing.T) {
	srv := serve(t, 200, `{"object":"list","data":[
		{"id":"qwen3:32b","object":"model","created":0,"owned_by":"library"},
		{"id":"deepseek-chat","object":"model","created":1767225600,"owned_by":"deepseek"}]}`)
	ms, err := NewOpenAI(OpenAIOptions{Name: "openai", Model: "x", APIKey: "k", BaseURL: srv.URL}).ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].ID != "deepseek-chat" || !ms[1].Created.IsZero() {
		t.Fatalf("models (dated first): %+v", ms)
	}
}

func TestModelNotFoundIsExplained(t *testing.T) {
	a := serve(t, 404, `{"type":"error","error":{"type":"not_found_error","message":"model: claude-old"}}`)
	_, err := NewAnthropic(AnthropicOptions{Name: "anthropic", Model: "claude-old", APIKey: "k", BaseURL: a.URL}).Chat(context.Background(), &Request{Messages: []Message{{Role: RoleUser, Text: "hi"}}})
	if !errors.Is(err, ErrModelNotFound) || !strings.Contains(err.Error(), `"claude-old" is not offered by anthropic`) || !strings.Contains(err.Error(), "yuanfang models fetch") {
		t.Fatalf("anthropic: %v", err)
	}
	o := serve(t, 404, `{"error":{"message":"The model does not exist","type":"invalid_request_error","code":"model_not_found"}}`)
	_, err = NewOpenAI(OpenAIOptions{Name: "deepseek", Model: "gone", APIKey: "k", BaseURL: o.URL}).Chat(context.Background(), &Request{Messages: []Message{{Role: RoleUser, Text: "hi"}}})
	if !errors.Is(err, ErrModelNotFound) || !strings.Contains(err.Error(), `"gone" is not offered by deepseek at `+o.URL) {
		t.Fatalf("openai: %v", err)
	}
	other := serve(t, 401, `{"type":"error","error":{"type":"authentication_error","message":"bad key"}}`)
	_, err = NewAnthropic(AnthropicOptions{Model: "m", APIKey: "k", BaseURL: other.URL}).Chat(context.Background(), &Request{Messages: []Message{{Role: RoleUser, Text: "hi"}}})
	if err == nil || errors.Is(err, ErrModelNotFound) {
		t.Fatalf("401 must not look like a missing model: %v", err)
	}
}

func TestOpenAIUsageSplitsCachedTokens(t *testing.T) {
	srv := serve(t, 200, `{"id":"c","object":"chat.completion","created":1,"model":"m",
		"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],
		"usage":{"prompt_tokens":1000,"completion_tokens":20,"total_tokens":1020,"prompt_tokens_details":{"cached_tokens":800}}}`)
	resp, err := NewOpenAI(OpenAIOptions{Model: "m", APIKey: "k", BaseURL: srv.URL}).Chat(context.Background(), &Request{Messages: []Message{{Role: RoleUser, Text: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	u := resp.Usage
	if u.InputTokens != 200 || u.CacheReadTokens != 800 || u.TotalInput() != 1000 {
		t.Fatalf("usage = %+v (total %d)", u, u.TotalInput())
	}
}

func TestAnthropicThinkingSummary(t *testing.T) {
	srv, body, _ := capture(t, `{"id":"m","type":"message","role":"assistant","model":"m",
		"content":[{"type":"thinking","thinking":"Check locks before IO.","signature":"sig"},
		           {"type":"redacted_thinking","data":"opaque"},
		           {"type":"text","text":"Checking locks."}],
		"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	req := &Request{Messages: []Message{{Role: RoleUser, Text: "hi"}}}

	resp, err := NewAnthropic(AnthropicOptions{Model: "m", APIKey: "k", BaseURL: srv.URL, ThinkingSummary: true}).Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	th, _ := (*body)["thinking"].(map[string]any)
	if th["type"] != "adaptive" || th["display"] != "summarized" {
		t.Errorf("thinking param = %v", (*body)["thinking"])
	}
	if resp.Message.Thinking != "Check locks before IO.\n\n[redacted thinking]" || resp.Message.Text != "Checking locks." {
		t.Errorf("thinking=%q text=%q", resp.Message.Thinking, resp.Message.Text)
	}

	clear(*body) // capture decodes into the same map; drop the first request's fields
	if _, err := NewAnthropic(AnthropicOptions{Model: "m", APIKey: "k", BaseURL: srv.URL}).Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, sent := (*body)["thinking"]; sent {
		t.Error("thinking param sent for a model not known to support it")
	}
}

func TestOpenAIReasoningContent(t *testing.T) {
	srv := serve(t, 200, `{"id":"c","object":"chat.completion","created":1,"model":"deepseek-reasoner",
		"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"done","reasoning_content":"First, the locks."}}],
		"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	resp, err := NewOpenAI(OpenAIOptions{Model: "m", APIKey: "k", BaseURL: srv.URL}).Chat(context.Background(), &Request{Messages: []Message{{Role: RoleUser, Text: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Thinking != "First, the locks." {
		t.Fatalf("thinking = %q", resp.Message.Thinking)
	}
}
