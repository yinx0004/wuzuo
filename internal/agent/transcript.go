// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"encoding/json"
	"time"

	"github.com/yinx0004/wuzuo/internal/llm"
)

// Recorder receives the transcript of an investigation as it happens, one
// entry per step of the conversation. Joining the "user" entries and the
// "assistant" entries in step order reproduces exactly what the model saw
// at every step.
type Recorder interface {
	Record(Entry)
}

// RecorderFunc adapts a function to Recorder.
type RecorderFunc func(Entry)

// Record implements Recorder.
func (f RecorderFunc) Record(e Entry) { f(e) }

// Transcript entry types written by the agent. The CLI adds "case" before
// "start" and "end" after the last entry.
const (
	EntryStart     = "start"     // provider, model, system prompt, tools
	EntryUser      = "user"      // content added to the conversation for a step
	EntryAssistant = "assistant" // the model's reply for a step
	EntryError     = "error"     // something that ended or disrupted a step
)

// Error kinds.
const (
	ErrorLLM            = "llm"             // the provider call failed
	ErrorRefused        = "refused"         // the model declined
	ErrorReportRejected = "report_rejected" // submit_autopsy failed validation
	ErrorNoVerdict      = "no_verdict"      // no accepted report within the limits
)

// Entry is one line of the transcript. Fields are set according to Type.
type Entry struct {
	Type string    `json:"type"`
	Step int       `json:"step,omitempty"`
	Time time.Time `json:"time"`

	// start
	Provider string     `json:"provider,omitempty"`
	Model    string     `json:"model,omitempty"`
	System   string     `json:"system,omitempty"`
	Tools    []ToolInfo `json:"tools,omitempty"`

	// user: text and/or results of the previous step's tool calls. Final
	// results belong to the last step and were not sent to the model.
	Text        string       `json:"text,omitempty"`
	ToolResults []ToolResult `json:"tool_results,omitempty"`
	Final       bool         `json:"final,omitempty"`

	// assistant (Text is shared with user)
	Thinking   string     `json:"thinking,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	StopReason string     `json:"stop_reason,omitempty"`
	StopDetail string     `json:"stop_detail,omitempty"`
	Usage      *Usage     `json:"usage,omitempty"`
	LatencyMS  int64      `json:"latency_ms,omitempty"`

	// error
	Kind   string `json:"kind,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// ToolInfo is a tool as offered to the model.
type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Properties  map[string]any `json:"properties,omitempty"`
	Required    []string       `json:"required,omitempty"`
}

// ToolCall is a tool call requested by the model.
type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// ToolResult is exactly what was returned to the model for one call.
type ToolResult struct {
	CallID   string `json:"call_id"`
	Tool     string `json:"tool"`
	Evidence string `json:"evidence,omitempty"` // evidence ID, for probes
	IsError  bool   `json:"is_error,omitempty"`
	Content  string `json:"content"`
}

// Usage is token usage for one step; the input fields do not overlap.
type Usage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read,omitempty"`
	CacheWrite int64 `json:"cache_write,omitempty"`
}

func (inv *investigation) record(e Entry) {
	if inv.opts.Recorder == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	inv.opts.Recorder.Record(e)
}

func startEntry(p llm.Provider, specs []llm.ToolSpec) Entry {
	tools := make([]ToolInfo, len(specs))
	for i, s := range specs {
		tools[i] = ToolInfo{Name: s.Name, Description: s.Description, Properties: s.Properties, Required: s.Required}
	}
	return Entry{Type: EntryStart, Provider: p.Name(), Model: p.Model(), System: systemPrompt, Tools: tools}
}

func assistantEntry(step int, resp *llm.Response, latency time.Duration) Entry {
	e := Entry{
		Type:       EntryAssistant,
		Step:       step,
		Text:       resp.Message.Text,
		Thinking:   resp.Message.Thinking,
		StopReason: string(resp.StopReason),
		StopDetail: resp.StopDetail,
		LatencyMS:  latency.Milliseconds(),
		Usage: &Usage{
			Input:      resp.Usage.InputTokens,
			Output:     resp.Usage.OutputTokens,
			CacheRead:  resp.Usage.CacheReadTokens,
			CacheWrite: resp.Usage.CacheWriteTokens,
		},
	}
	for _, c := range resp.Message.ToolCalls {
		e.ToolCalls = append(e.ToolCalls, ToolCall{ID: c.ID, Name: c.Name, Args: compactJSON(c.Args)})
	}
	return e
}
