// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/yinx0004/yuanfang/internal/agent"
	"github.com/yinx0004/yuanfang/internal/cases"
	"github.com/yinx0004/yuanfang/pkg/toolset"
)

// clip limits for the default (non --full) transcript view.
const (
	clipText    = 800
	clipResult  = 240
	clipPrompts = 400
)

// printTranscript renders a case transcript as a step-by-step timeline.
func printTranscript(w io.Writer, lines []json.RawMessage, full bool) error {
	clip := func(s string, n int) string {
		s = strings.TrimSpace(s)
		if full {
			return s
		}
		return toolset.Clip(s, n)
	}
	indent := func(s string) string { return "    " + strings.ReplaceAll(s, "\n", "\n    ") }

	for _, raw := range lines {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			return fmt.Errorf("transcript: %w", err)
		}
		switch head.Type {
		case cases.EntryCase:
			var e cases.CaseEntry
			if err := json.Unmarshal(raw, &e); err != nil {
				return err
			}
			fmt.Fprintf(w, "case %s (yuanfang %s), started %s\n", e.CaseID, e.Version, e.Time.Format("2006-01-02 15:04:05 MST"))
			if e.ReplayOf != "" {
				fmt.Fprintf(w, "re-examines case %s\n", e.ReplayOf)
			}
			fmt.Fprintf(w, "complaint: %s\n", e.Question)
			if len(e.Targets) > 0 {
				fmt.Fprintf(w, "targets: %s\n", strings.Join(e.Targets, ", "))
			}
			fmt.Fprintf(w, "settings: %s\n", formatSettings(e.Settings))

		case cases.EntryEnd:
			var e cases.EndEntry
			if err := json.Unmarshal(raw, &e); err != nil {
				return err
			}
			fmt.Fprintf(w, "\nend: %s after %s, %s\n", e.Status, plural(e.Steps, "step"), tokenSummary(e.Tokens))
			if e.Error != "" {
				fmt.Fprintf(w, "error: %s\n", e.Error)
			}

		default:
			var e agent.Entry
			if err := json.Unmarshal(raw, &e); err != nil {
				return err
			}
			printAgentEntry(w, e, clip, indent)
		}
	}
	return nil
}

func printAgentEntry(w io.Writer, e agent.Entry, clip func(string, int) string, indent func(string) string) {
	switch e.Type {
	case agent.EntryStart:
		names := make([]string, len(e.Tools))
		for i, t := range e.Tools {
			names[i] = t.Name
		}
		fmt.Fprintf(w, "model: %s/%s\n", e.Provider, e.Model)
		fmt.Fprintf(w, "system prompt: %d characters; tools (%d): %s\n", len(e.System), len(e.Tools), strings.Join(names, ", "))

	case agent.EntryUser:
		if e.Step == 1 {
			fmt.Fprintf(w, "\n── step 1 ──\n  prompt\n%s\n", indent(clip(e.Text, clipPrompts)))
			return
		}
		if e.Final {
			fmt.Fprintln(w, "  results (final step; not sent to the model)")
		} else {
			fmt.Fprintf(w, "\n── step %d ──\n", e.Step)
			if len(e.ToolResults) > 0 {
				fmt.Fprintln(w, "  results")
			}
		}
		for _, r := range e.ToolResults {
			label := r.Tool
			if r.Evidence != "" {
				label = r.Evidence + " " + r.Tool
			}
			if r.IsError {
				label += " (error)"
			}
			content := r.Content
			if r.Evidence != "" {
				// Probe results start with a header line; show the size of the rest.
				content = fmt.Sprintf("%s [%d characters]", content, len([]rune(r.Content)))
			}
			fmt.Fprintf(w, "    %s: %s\n", label, strings.ReplaceAll(clip(content, clipResult), "\n", " "))
		}
		if e.Text != "" {
			fmt.Fprintf(w, "  yuanfang\n%s\n", indent(clip(e.Text, clipText)))
		}

	case agent.EntryAssistant:
		meta := []string{fmt.Sprintf("%.1fs", float64(e.LatencyMS)/1000)}
		if e.Usage != nil {
			meta = append(meta, tokenSummary(cases.Tokens{Input: e.Usage.Input, Output: e.Usage.Output, CacheRead: e.Usage.CacheRead, CacheWrite: e.Usage.CacheWrite}))
		}
		if e.StopReason != "" && e.StopReason != "tool_use" && e.StopReason != "end_turn" {
			meta = append(meta, "stop: "+strings.TrimSpace(e.StopReason+" "+e.StopDetail))
		}
		fmt.Fprintf(w, "  model reply (%s)\n", strings.Join(meta, ", "))
		if e.Thinking != "" {
			fmt.Fprintf(w, "  thinking\n%s\n", indent(clip(e.Thinking, clipText)))
		}
		if strings.TrimSpace(e.Text) != "" {
			fmt.Fprintf(w, "  says\n%s\n", indent(clip(e.Text, clipText)))
		}
		if len(e.ToolCalls) > 0 {
			fmt.Fprintln(w, "  calls")
			for _, c := range e.ToolCalls {
				fmt.Fprintf(w, "    %s %s\n", c.Name, clip(string(c.Args), clipResult))
			}
		}

	case agent.EntryError:
		fmt.Fprintf(w, "  ! %s: %s\n", e.Kind, clip(e.Detail, clipText))
	}
}

func formatSettings(s map[string]any) string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%v", k, s[k])
	}
	return strings.Join(parts, " ")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
