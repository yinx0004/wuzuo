// SPDX-License-Identifier: Apache-2.0

// Package agent runs an investigation: the model proposes hypotheses, calls
// read-only probes whose outputs are sealed as evidence, rules hypotheses out,
// and finally submits an autopsy report that is validated against the
// evidence locker before it is accepted.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yinx0004/wuzuo/internal/cases"
	"github.com/yinx0004/wuzuo/internal/evidence"
	"github.com/yinx0004/wuzuo/internal/llm"
	"github.com/yinx0004/wuzuo/internal/report"
	"github.com/yinx0004/wuzuo/pkg/toolset"
)

// Target is a system the investigator may examine.
type Target struct {
	Name, Type, Description string
}

// Input describes what to investigate.
type Input struct {
	Question string
	Targets  []Target
	// Replay, when non-nil, re-examines previously sealed evidence without
	// collecting anything new.
	Replay        []*evidence.Evidence
	ReplayExcerpt int
}

// EventKind classifies progress events.
type EventKind string

// Event kinds.
const (
	EventNarration  EventKind = "narration"
	EventProbe      EventKind = "probe"
	EventEvidence   EventKind = "evidence"
	EventHypothesis EventKind = "hypothesis"
	EventRuledOut   EventKind = "ruled_out"
	EventRejected   EventKind = "rejected"
)

// Event reports progress to the caller (CLI, server, bot).
type Event struct {
	Kind     EventKind
	Step     int
	Text     string
	Evidence *evidence.Evidence
}

// Options configures an Investigator.
type Options struct {
	Provider         llm.Provider
	Tools            []toolset.Tool
	Locker           *evidence.Locker
	MaxSteps         int
	MaxEvidenceChars int
	// OnEvent, if set, receives progress events. It must not block for long.
	OnEvent func(Event)
}

// Result is the outcome of an investigation.
type Result struct {
	Autopsy    *report.Autopsy
	Hypotheses []cases.Hypothesis
	Steps      int
	Usage      llm.Usage
}

// ErrNoVerdict is returned when the model never submits an acceptable report.
var ErrNoVerdict = errors.New("investigation ended without an accepted autopsy report")

const maxNudges = 3

// Investigate runs the loop until a valid autopsy is submitted.
func Investigate(ctx context.Context, opts Options, in Input) (*Result, error) {
	if opts.Provider == nil || opts.Locker == nil {
		return nil, errors.New("agent: provider and locker are required")
	}
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = 40
	}
	inv := &investigation{
		opts:  opts,
		tools: map[string]toolset.Tool{},
		res:   &Result{},
	}
	var specs []llm.ToolSpec
	if in.Replay == nil {
		for _, t := range opts.Tools {
			s := t.Spec()
			if err := toolset.ValidateSpec(s); err != nil {
				return nil, err
			}
			if _, dup := inv.tools[s.Name]; dup {
				return nil, fmt.Errorf("duplicate tool name %q", s.Name)
			}
			inv.tools[s.Name] = t
			specs = append(specs, llm.ToolSpec{Name: s.Name, Description: s.Description, Properties: s.Parameters.Properties, Required: s.Parameters.Required})
		}
	}
	specs = append(specs, metaToolSpecs()...)

	msgs := []llm.Message{{Role: llm.RoleUser, Text: initialPrompt(in, time.Now())}}
	nudges := 0
	for step := 1; ; step++ {
		if step > opts.MaxSteps+2 {
			return inv.res, ErrNoVerdict
		}
		resp, err := opts.Provider.Chat(ctx, &llm.Request{System: systemPrompt, Messages: msgs, Tools: specs})
		if err != nil {
			return inv.res, err
		}
		inv.res.Steps = step
		inv.res.Usage.Add(resp.Usage)
		msgs = append(msgs, resp.Message)
		if t := strings.TrimSpace(resp.Message.Text); t != "" {
			inv.emit(Event{Kind: EventNarration, Step: step, Text: t})
		}
		if resp.StopReason == llm.StopRefusal {
			return inv.res, fmt.Errorf("%w: %s", llm.ErrRefused, resp.StopDetail)
		}

		var nudge string
		if len(resp.Message.ToolCalls) == 0 {
			nudges++
			if nudges > maxNudges {
				return inv.res, ErrNoVerdict
			}
			nudge = "Continue the investigation. When the verdict is clear, call submit_autopsy; the report only counts once submitted through that tool."
			if resp.StopReason == llm.StopMaxTokens {
				nudge = "Your reply was cut off. Be more concise and continue with tool calls."
			}
			msgs = append(msgs, llm.Message{Role: llm.RoleUser, Text: nudge})
			continue
		}

		results, done := inv.runCalls(ctx, step, resp.Message.ToolCalls)
		if done {
			return inv.res, nil
		}
		if step == opts.MaxSteps {
			nudge = "Step budget exhausted. Call submit_autopsy now with the evidence you have; mark anything unsupported as low confidence and list what remains unknown in open_questions."
		}
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, ToolResults: results, Text: nudge})
	}
}

type investigation struct {
	opts  Options
	tools map[string]toolset.Tool
	res   *Result
	mu    sync.Mutex
}

func (inv *investigation) emit(e Event) {
	if inv.opts.OnEvent != nil {
		inv.opts.OnEvent(e)
	}
}

type probeOutcome struct {
	result  *toolset.Result
	err     error
	started time.Time
}

// runCalls executes one assistant turn's tool calls. Probes run concurrently;
// evidence is sealed in call order so IDs are deterministic.
func (inv *investigation) runCalls(ctx context.Context, step int, calls []llm.ToolCall) ([]llm.ToolResult, bool) {
	outcomes := make([]*probeOutcome, len(calls))
	var wg sync.WaitGroup
	for i, c := range calls {
		tool, ok := inv.tools[c.Name]
		if !ok {
			continue
		}
		inv.emit(Event{Kind: EventProbe, Step: step, Text: describeCall(c)})
		wg.Add(1)
		go func(i int, c llm.ToolCall, tool toolset.Tool) {
			defer wg.Done()
			o := &probeOutcome{started: time.Now()}
			o.result, o.err = tool.Run(ctx, c.Args)
			outcomes[i] = o
		}(i, c, tool)
	}
	wg.Wait()

	results := make([]llm.ToolResult, len(calls))
	done := false
	for i, c := range calls {
		if o := outcomes[i]; o != nil {
			results[i] = inv.seal(step, c, o)
			continue
		}
		content, isErr, accepted := inv.runMeta(step, c)
		results[i] = llm.ToolResult{CallID: c.ID, Content: content, IsError: isErr}
		done = done || accepted
	}
	return results, done
}

func (inv *investigation) seal(step int, c llm.ToolCall, o *probeOutcome) llm.ToolResult {
	target, content := "", ""
	if o.result != nil {
		target, content = o.result.Target, o.result.Content
	}
	if target == "" {
		target = argTarget(c.Args)
	}
	e, err := inv.opts.Locker.Record(c.Name, target, compactJSON(c.Args), content, o.err, o.started)
	if err != nil {
		return llm.ToolResult{CallID: c.ID, Content: "failed to seal evidence: " + err.Error(), IsError: true}
	}
	inv.emit(Event{Kind: EventEvidence, Step: step, Evidence: e})
	return llm.ToolResult{CallID: c.ID, Content: formatEvidence(e, inv.opts.MaxEvidenceChars), IsError: o.err != nil}
}

func describeCall(c llm.ToolCall) string {
	args := string(compactJSON(c.Args))
	if args == "{}" || args == "" {
		return c.Name
	}
	return c.Name + " " + args
}

func argTarget(raw json.RawMessage) string {
	var v struct {
		Target string `json:"target"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Target
}

func compactJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, _ := json.Marshal(v)
	return out
}
