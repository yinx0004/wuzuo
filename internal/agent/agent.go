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
	// Recorder, if set, receives the full transcript as it happens.
	Recorder Recorder
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

	inv.record(startEntry(opts.Provider, specs))
	first := llm.Message{Role: llm.RoleUser, Text: initialPrompt(in, time.Now())}
	inv.record(Entry{Type: EntryUser, Step: 1, Text: first.Text})
	msgs := []llm.Message{first}
	nudges := 0
	for step := 1; ; step++ {
		if step > opts.MaxSteps+2 {
			inv.record(Entry{Type: EntryError, Step: step, Kind: ErrorNoVerdict, Detail: "step limit reached"})
			return inv.res, ErrNoVerdict
		}
		started := time.Now()
		resp, err := opts.Provider.Chat(ctx, &llm.Request{System: systemPrompt, Messages: msgs, Tools: specs})
		if err != nil {
			inv.record(Entry{Type: EntryError, Step: step, Kind: ErrorLLM, Detail: err.Error()})
			return inv.res, err
		}
		inv.record(assistantEntry(step, resp, time.Since(started)))
		inv.res.Steps = step
		inv.res.Usage.Add(resp.Usage)
		msgs = append(msgs, resp.Message)
		if t := strings.TrimSpace(resp.Message.Text); t != "" {
			inv.emit(Event{Kind: EventNarration, Step: step, Text: t})
		}
		if resp.StopReason == llm.StopRefusal {
			inv.record(Entry{Type: EntryError, Step: step, Kind: ErrorRefused, Detail: resp.StopDetail})
			return inv.res, fmt.Errorf("%w: %s", llm.ErrRefused, resp.StopDetail)
		}

		var nudge string
		if len(resp.Message.ToolCalls) == 0 {
			nudges++
			if nudges > maxNudges {
				inv.record(Entry{Type: EntryError, Step: step, Kind: ErrorNoVerdict, Detail: "the model kept replying without tool calls"})
				return inv.res, ErrNoVerdict
			}
			nudge = "Continue the investigation. When the verdict is clear, call submit_autopsy; the report only counts once submitted through that tool."
			if resp.StopReason == llm.StopMaxTokens {
				nudge = "Your reply was cut off. Be more concise and continue with tool calls."
			}
			inv.record(Entry{Type: EntryUser, Step: step + 1, Text: nudge})
			msgs = append(msgs, llm.Message{Role: llm.RoleUser, Text: nudge})
			continue
		}

		results, evidenceIDs, done := inv.runCalls(ctx, step, resp.Message.ToolCalls)
		recorded := toolResultEntries(resp.Message.ToolCalls, results, evidenceIDs)
		if done {
			inv.record(Entry{Type: EntryUser, Step: step + 1, ToolResults: recorded, Final: true})
			return inv.res, nil
		}
		if step == opts.MaxSteps {
			nudge = "Step budget exhausted. Call submit_autopsy now with the evidence you have; mark anything unsupported as low confidence and list what remains unknown in open_questions."
		}
		inv.record(Entry{Type: EntryUser, Step: step + 1, ToolResults: recorded, Text: nudge})
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, ToolResults: results, Text: nudge})
	}
}

func toolResultEntries(calls []llm.ToolCall, results []llm.ToolResult, evidenceIDs []string) []ToolResult {
	out := make([]ToolResult, len(results))
	for i, r := range results {
		out[i] = ToolResult{CallID: r.CallID, Tool: calls[i].Name, Evidence: evidenceIDs[i], IsError: r.IsError, Content: r.Content}
	}
	return out
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
	result            *toolset.Result
	err               error
	started, finished time.Time
}

// runCalls executes one assistant turn's tool calls. Probes run concurrently;
// evidence is sealed in call order so IDs are deterministic. It returns the
// results in call order, the evidence ID sealed for each probe call ("" for
// meta tools), and whether a report was accepted.
func (inv *investigation) runCalls(ctx context.Context, step int, calls []llm.ToolCall) ([]llm.ToolResult, []string, bool) {
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
			o.finished = time.Now() // not at sealing time, which waits for the slowest probe
			outcomes[i] = o
		}(i, c, tool)
	}
	wg.Wait()

	results := make([]llm.ToolResult, len(calls))
	evidenceIDs := make([]string, len(calls))
	done := false
	for i, c := range calls {
		if o := outcomes[i]; o != nil {
			results[i], evidenceIDs[i] = inv.seal(step, c, o)
			continue
		}
		content, isErr, accepted := inv.runMeta(step, c)
		results[i] = llm.ToolResult{CallID: c.ID, Content: content, IsError: isErr}
		done = done || accepted
	}
	return results, evidenceIDs, done
}

func (inv *investigation) seal(step int, c llm.ToolCall, o *probeOutcome) (llm.ToolResult, string) {
	target, content := "", ""
	if o.result != nil {
		target, content = o.result.Target, o.result.Content
	}
	if target == "" {
		target = argTarget(c.Args)
	}
	e, err := inv.opts.Locker.Record(c.Name, target, compactJSON(c.Args), content, o.err, o.started, o.finished)
	if err != nil {
		return llm.ToolResult{CallID: c.ID, Content: "failed to seal evidence: " + err.Error(), IsError: true}, ""
	}
	inv.emit(Event{Kind: EventEvidence, Step: step, Evidence: e})
	return llm.ToolResult{CallID: c.ID, Content: formatEvidence(e, inv.opts.MaxEvidenceChars), IsError: o.err != nil}, e.ID
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
