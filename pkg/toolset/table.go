// SPDX-License-Identifier: Apache-2.0

package toolset

import (
	"fmt"
	"strings"
)

// Table renders rows as a pipe-separated table, which LLMs read reliably and
// which stays compact compared to JSON.
func Table(columns []string, rows [][]string) string {
	if len(rows) == 0 {
		return "(no rows)\n"
	}
	var b strings.Builder
	b.WriteString(strings.Join(columns, " | "))
	b.WriteByte('\n')
	for _, r := range rows {
		cells := make([]string, len(r))
		for i, c := range r {
			cells[i] = oneLine(c)
		}
		b.WriteString(strings.Join(cells, " | "))
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "(%d rows)\n", len(rows))
	return b.String()
}

// KV renders ordered key/value pairs one per line.
func KV(pairs [][2]string) string {
	var b strings.Builder
	for _, p := range pairs {
		fmt.Fprintf(&b, "%s: %s\n", p[0], oneLine(p[1]))
	}
	return b.String()
}

// Clip shortens s to at most n runes, marking the cut.
func Clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "¦")
	return s
}
