// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yinx0004/yuanfang/internal/cases"
	"github.com/yinx0004/yuanfang/internal/llm"
	"github.com/yinx0004/yuanfang/internal/report"
)

// Meta tools structure the examination itself; they do not touch any system.
const (
	toolPropose      = "propose_hypothesis"
	toolRuleOut      = "rule_out"
	toolReadEvidence = "read_evidence"
	toolSubmit       = "submit_diagnosis"
)

func metaToolSpecs() []llm.ToolSpec {
	evidenceIDs := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	diagnosisProps, diagnosisRequired := report.Schema()
	return []llm.ToolSpec{
		{
			Name:        toolPropose,
			Description: "Record a candidate cause to investigate. Propose several plausible hypotheses early; each gets an ID such as H2.",
			Properties: map[string]any{
				"name":      map[string]any{"type": "string", "description": "Short name, e.g. 'Row lock contention on orders'."},
				"rationale": map[string]any{"type": "string", "description": "Why this is plausible given the complaint."},
			},
			Required: []string{"name"},
		},
		{
			Name:        toolRuleOut,
			Description: "Eliminate a hypothesis. You must cite the evidence IDs that contradict it.",
			Properties: map[string]any{
				"hypothesis": map[string]any{"type": "string", "description": "Hypothesis ID (H1) or name."},
				"reason":     map[string]any{"type": "string"},
				"evidence":   evidenceIDs,
			},
			Required: []string{"hypothesis", "reason", "evidence"},
		},
		{
			Name:        toolReadEvidence,
			Description: "Read sealed evidence that was cut off in an earlier result. Returns up to 'limit' characters starting at 'offset'.",
			Properties: map[string]any{
				"id":     map[string]any{"type": "string", "description": "Evidence ID, e.g. E3."},
				"offset": map[string]any{"type": "integer", "minimum": 0},
				"limit":  map[string]any{"type": "integer", "minimum": 1000, "maximum": 50000},
			},
			Required: []string{"id"},
		},
		{
			Name:        toolSubmit,
			Description: "Submit the final diagnosis report. It is checked against the evidence locker: every cited ID must exist, the root cause must cite evidence unless its confidence is low, and ruled-out hypotheses must cite evidence. If rejected, fix the listed problems and submit again.",
			Properties:  diagnosisProps,
			Required:    diagnosisRequired,
		},
	}
}

// runMeta executes a meta tool. accepted is true when a report was accepted.
func (inv *investigation) runMeta(step int, c llm.ToolCall) (content string, isErr, accepted bool) {
	switch c.Name {
	case toolPropose:
		var a struct{ Name, Rationale string }
		if err := json.Unmarshal(nonEmpty(c.Args), &a); err != nil || strings.TrimSpace(a.Name) == "" {
			return "invalid arguments: name is required", true, false
		}
		inv.mu.Lock()
		h := cases.Hypothesis{ID: fmt.Sprintf("H%d", len(inv.res.Hypotheses)+1), Name: a.Name, Rationale: a.Rationale, Status: "open"}
		inv.res.Hypotheses = append(inv.res.Hypotheses, h)
		inv.mu.Unlock()
		inv.emit(Event{Kind: EventHypothesis, Step: step, Text: h.ID + " " + h.Name})
		return fmt.Sprintf("Recorded %s: %s", h.ID, h.Name), false, false

	case toolRuleOut:
		var a struct {
			Hypothesis, Reason string
			Evidence           []string
		}
		if err := json.Unmarshal(nonEmpty(c.Args), &a); err != nil {
			return "invalid arguments: " + err.Error(), true, false
		}
		if len(a.Evidence) == 0 {
			return "rejected: cite at least one evidence ID that contradicts the hypothesis", true, false
		}
		if m := inv.opts.Locker.Missing(a.Evidence); len(m) > 0 {
			return "rejected: unknown evidence " + strings.Join(m, ", "), true, false
		}
		h := inv.findHypothesis(a.Hypothesis)
		if h == nil {
			return fmt.Sprintf("rejected: no hypothesis %q; propose it first", a.Hypothesis), true, false
		}
		inv.mu.Lock()
		h.Status, h.Reason, h.Evidence = "ruled_out", a.Reason, a.Evidence
		inv.mu.Unlock()
		inv.emit(Event{Kind: EventRuledOut, Step: step, Text: fmt.Sprintf("%s %s: %s", h.ID, h.Name, a.Reason)})
		return fmt.Sprintf("%s ruled out.", h.ID), false, false

	case toolReadEvidence:
		var a struct {
			ID            string
			Offset, Limit int
		}
		if err := json.Unmarshal(nonEmpty(c.Args), &a); err != nil {
			return "invalid arguments: " + err.Error(), true, false
		}
		e, ok := inv.opts.Locker.Get(a.ID)
		if !ok {
			return "unknown evidence " + a.ID, true, false
		}
		if a.Limit <= 0 {
			a.Limit = 20000
		}
		r := []rune(e.Content)
		if a.Offset >= len(r) {
			return fmt.Sprintf("%s has %d characters; offset %d is past the end", e.ID, len(r), a.Offset), true, false
		}
		end := min(a.Offset+a.Limit, len(r))
		return fmt.Sprintf("[%s] characters %d-%d of %d\n%s", e.ID, a.Offset, end, len(r), string(r[a.Offset:end])), false, false

	case toolSubmit:
		var a report.Diagnosis
		if err := json.Unmarshal(nonEmpty(c.Args), &a); err != nil {
			return "rejected: report is not valid JSON for the schema: " + err.Error(), true, false
		}
		if problems := a.Validate(inv.opts.Locker); len(problems) > 0 {
			inv.emit(Event{Kind: EventRejected, Step: step, Text: strings.Join(problems, "; ")})
			inv.record(Entry{Type: EntryError, Step: step, Kind: ErrorReportRejected, Detail: strings.Join(problems, "; ")})
			return "rejected, fix these problems and submit again:\n- " + strings.Join(problems, "\n- "), true, false
		}
		inv.res.Diagnosis = &a
		return "accepted", false, true

	default:
		return fmt.Sprintf("unknown tool %q", c.Name), true, false
	}
}

func (inv *investigation) findHypothesis(ref string) *cases.Hypothesis {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	ref = strings.TrimSpace(ref)
	for i := range inv.res.Hypotheses {
		h := &inv.res.Hypotheses[i]
		if strings.EqualFold(h.ID, ref) || strings.EqualFold(h.Name, ref) {
			return h
		}
	}
	return nil
}

func nonEmpty(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	return raw
}
