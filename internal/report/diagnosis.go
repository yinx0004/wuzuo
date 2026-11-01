// SPDX-License-Identifier: Apache-2.0

// Package report defines the diagnosis report: the structured verdict an
// investigation must produce, the rules it must satisfy, and its renderings.
package report

import (
	"fmt"
	"strings"
)

// Confidence levels.
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

// Finding is a claim backed by evidence.
type Finding struct {
	Statement  string   `json:"statement"`
	Confidence string   `json:"confidence"`
	Evidence   []string `json:"evidence"`
}

// RuledOut records a hypothesis that was eliminated, and why.
type RuledOut struct {
	Hypothesis string   `json:"hypothesis"`
	Reason     string   `json:"reason"`
	Evidence   []string `json:"evidence"`
}

// Event is a point on the reconstructed timeline.
type Event struct {
	Time        string   `json:"time"`
	Description string   `json:"description"`
	Evidence    []string `json:"evidence"`
}

// Action is a recommended remediation. Yuanfang never executes these.
type Action struct {
	Description string `json:"description"`
	// Risk of performing the action: low, medium, high.
	Risk string `json:"risk"`
	// Command is an optional exact command or statement for a human to review.
	Command string `json:"command,omitempty"`
}

// Diagnosis is the final verdict of an investigation.
type Diagnosis struct {
	Title           string     `json:"title"`
	Summary         string     `json:"summary"`
	RootCause       Finding    `json:"root_cause"`
	Contributing    []Finding  `json:"contributing_factors,omitempty"`
	RuledOut        []RuledOut `json:"ruled_out,omitempty"`
	Timeline        []Event    `json:"timeline,omitempty"`
	Recommendations []Action   `json:"recommendations,omitempty"`
	OpenQuestions   []string   `json:"open_questions,omitempty"`
}

// EvidenceChecker reports which evidence IDs do not exist.
type EvidenceChecker interface {
	Missing(ids []string) []string
}

// Validate enforces the evidence rules: every conclusion cites evidence that
// actually exists in the locker, and unsupported conclusions are labelled as
// low confidence. It returns human-readable problems for the model to fix.
func (a *Diagnosis) Validate(ev EvidenceChecker) []string {
	var p []string
	if strings.TrimSpace(a.Summary) == "" {
		p = append(p, "summary is empty")
	}
	if strings.TrimSpace(a.RootCause.Statement) == "" {
		p = append(p, "root_cause.statement is empty")
	}
	if !validConfidence(a.RootCause.Confidence) {
		p = append(p, fmt.Sprintf("root_cause.confidence %q must be high, medium or low", a.RootCause.Confidence))
	}
	if len(a.RootCause.Evidence) == 0 && a.RootCause.Confidence != ConfidenceLow {
		p = append(p, "root_cause cites no evidence: cite evidence IDs, or set confidence to low and say the cause is unconfirmed")
	}
	p = append(p, missing("root_cause", a.RootCause.Evidence, ev)...)
	for i, f := range a.Contributing {
		where := fmt.Sprintf("contributing_factors[%d]", i)
		if !validConfidence(f.Confidence) {
			p = append(p, where+".confidence must be high, medium or low")
		}
		if len(f.Evidence) == 0 && f.Confidence != ConfidenceLow {
			p = append(p, where+" cites no evidence")
		}
		p = append(p, missing(where, f.Evidence, ev)...)
	}
	for i, r := range a.RuledOut {
		where := fmt.Sprintf("ruled_out[%d] (%s)", i, r.Hypothesis)
		if len(r.Evidence) == 0 {
			p = append(p, where+": a hypothesis can only be ruled out with evidence")
		}
		p = append(p, missing(where, r.Evidence, ev)...)
	}
	for i, e := range a.Timeline {
		p = append(p, missing(fmt.Sprintf("timeline[%d]", i), e.Evidence, ev)...)
	}
	for i, r := range a.Recommendations {
		switch r.Risk {
		case "low", "medium", "high":
		default:
			p = append(p, fmt.Sprintf("recommendations[%d].risk %q must be low, medium or high", i, r.Risk))
		}
	}
	return p
}

func validConfidence(c string) bool {
	return c == ConfidenceHigh || c == ConfidenceMedium || c == ConfidenceLow
}

func missing(where string, ids []string, ev EvidenceChecker) []string {
	if ev == nil || len(ids) == 0 {
		return nil
	}
	if m := ev.Missing(ids); len(m) > 0 {
		return []string{fmt.Sprintf("%s cites unknown evidence %s", where, strings.Join(m, ", "))}
	}
	return nil
}

// Schema is the JSON Schema of Diagnosis, used as the submit_diagnosis tool input.
func Schema() (props map[string]any, required []string) {
	evidenceIDs := map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "string"},
		"description": "Evidence IDs (e.g. E3) supporting this item.",
	}
	confidence := map[string]any{"type": "string", "enum": []string{"high", "medium", "low"}}
	finding := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"statement":  map[string]any{"type": "string"},
			"confidence": confidence,
			"evidence":   evidenceIDs,
		},
		"required": []string{"statement", "confidence", "evidence"},
	}
	props = map[string]any{
		"title":      map[string]any{"type": "string", "description": "One-line incident title."},
		"summary":    map[string]any{"type": "string", "description": "2-4 sentences: what happened, why, and the impact."},
		"root_cause": finding,
		"contributing_factors": map[string]any{
			"type": "array", "items": finding,
			"description": "Conditions that made the incident possible or worse.",
		},
		"ruled_out": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"hypothesis": map[string]any{"type": "string"},
					"reason":     map[string]any{"type": "string"},
					"evidence":   evidenceIDs,
				},
				"required": []string{"hypothesis", "reason", "evidence"},
			},
		},
		"timeline": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"time":        map[string]any{"type": "string", "description": "Timestamp or relative time as observed."},
					"description": map[string]any{"type": "string"},
					"evidence":    evidenceIDs,
				},
				"required": []string{"time", "description", "evidence"},
			},
		},
		"recommendations": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"description": map[string]any{"type": "string"},
					"risk":        map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}},
					"command":     map[string]any{"type": "string", "description": "Optional exact command for a human to review. Never executed by Yuanfang."},
				},
				"required": []string{"description", "risk"},
			},
		},
		"open_questions": map[string]any{
			"type": "array", "items": map[string]any{"type": "string"},
			"description": "What could not be determined and what evidence would settle it.",
		},
	}
	return props, []string{"title", "summary", "root_cause"}
}
