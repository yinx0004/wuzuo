// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yinx0004/wuzuo/internal/evidence"
	"github.com/yinx0004/wuzuo/internal/llm"
	"github.com/yinx0004/wuzuo/pkg/toolset"
)

// scripted replays canned assistant turns and records what it was sent.
type scripted struct {
	turns    []llm.Message
	requests []*llm.Request
}

func (s *scripted) Name() string  { return "fake" }
func (s *scripted) Model() string { return "fake-1" }
func (s *scripted) Chat(_ context.Context, req *llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	if len(s.turns) == 0 {
		return nil, errors.New("script exhausted")
	}
	m := s.turns[0]
	s.turns = s.turns[1:]
	stop := llm.StopEndTurn
	if len(m.ToolCalls) > 0 {
		stop = llm.StopToolUse
	}
	m.Role = llm.RoleAssistant
	return &llm.Response{Message: m, StopReason: stop, Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}}, nil
}

type staticTool struct {
	name, out string
	err       error
}

func (t staticTool) Spec() toolset.Spec {
	return toolset.Spec{Name: t.name, Description: "test probe", Parameters: toolset.Schema{Properties: map[string]any{"target": map[string]any{"type": "string"}}}}
}
func (t staticTool) Run(context.Context, json.RawMessage) (*toolset.Result, error) {
	return &toolset.Result{Target: "db1", Content: t.out}, t.err
}

func call(id, name, args string) llm.ToolCall {
	return llm.ToolCall{ID: id, Name: name, Args: json.RawMessage(args)}
}

func TestInvestigateHappyPathWithRejection(t *testing.T) {
	p := &scripted{turns: []llm.Message{
		{Text: "Let me look.", ToolCalls: []llm.ToolCall{
			call("1", "propose_hypothesis", `{"name":"Lock contention"}`),
			call("2", "propose_hypothesis", `{"name":"Replication lag"}`),
			call("3", "probe_locks", `{"target":"db1"}`),
			call("4", "probe_repl", `{"target":"db1"}`),
		}},
		{ToolCalls: []llm.ToolCall{
			call("5", "rule_out", `{"hypothesis":"H2","reason":"lag 0","evidence":["E2"]}`),
			// Cites evidence that does not exist: must be rejected.
			call("6", "submit_autopsy", `{"title":"t","summary":"s","cause_of_death":{"statement":"row lock","confidence":"high","evidence":["E9"]}}`),
		}},
		{ToolCalls: []llm.ToolCall{
			call("7", "submit_autopsy", `{"title":"t","summary":"s","cause_of_death":{"statement":"row lock","confidence":"high","evidence":["E1"]},"ruled_out":[{"hypothesis":"Replication lag","reason":"lag 0","evidence":["E2"]}]}`),
		}},
	}}
	locker, _ := evidence.NewLocker("", true)
	var events []Event
	res, err := Investigate(context.Background(), Options{
		Provider: p,
		Tools:    []toolset.Tool{staticTool{name: "probe_locks", out: "blocking pid 42"}, staticTool{name: "probe_repl", out: "lag 0"}},
		Locker:   locker,
		OnEvent:  func(e Event) { events = append(events, e) },
	}, Input{Question: "writes hang", Targets: []Target{{Name: "db1", Type: "mysql"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Autopsy == nil || res.Autopsy.CauseOfDeath.Evidence[0] != "E1" {
		t.Fatalf("unexpected autopsy: %+v", res.Autopsy)
	}
	if res.Steps != 3 || res.Usage.InputTokens != 30 {
		t.Fatalf("steps=%d usage=%+v", res.Steps, res.Usage)
	}
	if len(res.Hypotheses) != 2 || res.Hypotheses[1].Status != "ruled_out" {
		t.Fatalf("hypotheses: %+v", res.Hypotheses)
	}
	all := locker.All()
	if len(all) != 2 || all[0].Tool != "probe_locks" || all[1].Tool != "probe_repl" {
		t.Fatalf("evidence order: %+v", all)
	}

	// The rejection must have been reported back to the model.
	second := p.requests[2].Messages
	lastUser := second[len(second)-1]
	var rejected bool
	for _, r := range lastUser.ToolResults {
		if r.IsError && strings.Contains(r.Content, "unknown evidence E9") {
			rejected = true
		}
	}
	if !rejected {
		t.Fatalf("rejection not sent back: %+v", lastUser.ToolResults)
	}
	var sawRejectEvent bool
	for _, e := range events {
		sawRejectEvent = sawRejectEvent || e.Kind == EventRejected
	}
	if !sawRejectEvent {
		t.Fatal("no rejected event emitted")
	}
}

func TestInvestigateFailedProbeIsEvidence(t *testing.T) {
	p := &scripted{turns: []llm.Message{
		{ToolCalls: []llm.ToolCall{call("1", "probe_locks", `{}`)}},
		{ToolCalls: []llm.ToolCall{call("2", "submit_autopsy", `{"title":"t","summary":"s","cause_of_death":{"statement":"cannot tell: access denied","confidence":"low","evidence":["E1"]}}`)}},
	}}
	locker, _ := evidence.NewLocker("", true)
	_, err := Investigate(context.Background(), Options{
		Provider: p,
		Tools:    []toolset.Tool{staticTool{name: "probe_locks", err: errors.New("access denied; need PROCESS privilege")}},
		Locker:   locker,
	}, Input{Question: "q"})
	if err != nil {
		t.Fatal(err)
	}
	e, _ := locker.Get("E1")
	if e == nil || !strings.Contains(e.Error, "PROCESS") {
		t.Fatalf("failed probe not sealed: %+v", e)
	}
	res := p.requests[1].Messages[2].ToolResults[0]
	if !res.IsError || !strings.Contains(res.Content, "PROBE FAILED") {
		t.Fatalf("tool result: %+v", res)
	}
}

func TestInvestigateGivesUpWithoutVerdict(t *testing.T) {
	p := &scripted{}
	for i := 0; i < maxNudges+1; i++ {
		p.turns = append(p.turns, llm.Message{Text: "thinking out loud"})
	}
	locker, _ := evidence.NewLocker("", true)
	_, err := Investigate(context.Background(), Options{Provider: p, Locker: locker}, Input{Question: "q"})
	if !errors.Is(err, ErrNoVerdict) {
		t.Fatalf("err = %v", err)
	}
}

type sleepyTool struct {
	name  string
	delay time.Duration
}

func (t sleepyTool) Spec() toolset.Spec {
	return toolset.Spec{Name: t.name, Description: "test probe"}
}
func (t sleepyTool) Run(context.Context, json.RawMessage) (*toolset.Result, error) {
	time.Sleep(t.delay)
	return &toolset.Result{Content: "ok"}, nil
}

// Probes in one turn run concurrently; each must record its own run time,
// not the time until the slowest probe finished.
func TestConcurrentProbesRecordOwnDuration(t *testing.T) {
	p := &scripted{turns: []llm.Message{
		{ToolCalls: []llm.ToolCall{call("1", "fast", `{}`), call("2", "slow", `{}`)}},
		{ToolCalls: []llm.ToolCall{call("3", "submit_autopsy", `{"title":"t","summary":"s","cause_of_death":{"statement":"x","confidence":"low","evidence":["E1"]}}`)}},
	}}
	locker, _ := evidence.NewLocker("", true)
	_, err := Investigate(context.Background(), Options{
		Provider: p,
		Tools:    []toolset.Tool{sleepyTool{"fast", 0}, sleepyTool{"slow", 300 * time.Millisecond}},
		Locker:   locker,
	}, Input{Question: "q"})
	if err != nil {
		t.Fatal(err)
	}
	fast, _ := locker.Get("E1")
	slow, _ := locker.Get("E2")
	if fast.DurationMS > 100 || slow.DurationMS < 290 {
		t.Fatalf("fast=%dms slow=%dms", fast.DurationMS, slow.DurationMS)
	}
}
