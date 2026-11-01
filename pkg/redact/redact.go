// SPDX-License-Identifier: Apache-2.0

// Package redact removes sensitive values from diagnostic output before it is
// shown to an LLM or written to the evidence locker.
//
// Two layers are provided: SQL normalizes statement text so literal values
// (which frequently contain customer data) never leave the database host, and
// Secrets strips credentials that may appear in any text.
package redact

import (
	"regexp"
	"strings"
)

// SQL replaces string, numeric, hex and boolean-like literals in a SQL
// statement with '?', similar to MySQL's statement digests. Identifiers,
// keywords and structure are preserved so the statement remains diagnosable.
func SQL(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	rs := []rune(s)
	n := len(rs)
	for i := 0; i < n; i++ {
		c := rs[i]
		switch {
		case c == '\'' || c == '"':
			// Quoted string literal. Handle doubled quotes and backslash escapes.
			j := i + 1
			for j < n {
				if rs[j] == '\\' && j+1 < n {
					j += 2
					continue
				}
				if rs[j] == c {
					if j+1 < n && rs[j+1] == c {
						j += 2
						continue
					}
					break
				}
				j++
			}
			b.WriteRune('?')
			i = j
		case c == '`':
			// Quoted identifier: keep verbatim.
			j := i + 1
			for j < n && rs[j] != '`' {
				j++
			}
			if j >= n {
				j = n - 1
			}
			b.WriteString(string(rs[i : j+1]))
			i = j
		case isDigit(c) && (i == 0 || !isIdentRune(rs[i-1])):
			j := i
			if c == '0' && i+1 < n && (rs[i+1] == 'x' || rs[i+1] == 'X' || rs[i+1] == 'b' || rs[i+1] == 'B') {
				j += 2
			}
			for j < n && (isHexDigit(rs[j]) || rs[j] == '.' || rs[j] == 'e' || rs[j] == 'E') {
				j++
			}
			if j < n && isIdentRune(rs[j]) {
				// Part of an identifier such as t1abc; keep as-is.
				b.WriteString(string(rs[i:j]))
				i = j - 1
				continue
			}
			b.WriteRune('?')
			i = j - 1
		default:
			b.WriteRune(c)
		}
	}
	return collapseInLists(b.String())
}

var inList = regexp.MustCompile(`\(\s*\?(\s*,\s*\?)+\s*\)`)

// collapseInLists turns "(?, ?, ?, ?)" into "(?...)" to keep output short.
func collapseInLists(s string) string {
	return inList.ReplaceAllString(s, "(?...)")
}

func isDigit(r rune) bool    { return r >= '0' && r <= '9' }
func isHexDigit(r rune) bool { return isDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') }
func isIdentRune(r rune) bool {
	return r == '_' || r == '$' || isDigit(r) || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127
}

var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	// IDENTIFIED BY 'secret' / PASSWORD('secret')
	{regexp.MustCompile(`(?i)(identified\s+(?:with\s+\S+\s+)?by\s+)('[^']*'|"[^"]*")`), "${1}'<redacted>'"},
	{regexp.MustCompile(`(?i)(password\s*\(\s*)('[^']*'|"[^"]*")`), "${1}'<redacted>'"},
	// key=value / key: value style credentials
	{regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key)(\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;&]+)`), "${1}${2}<redacted>"},
	// URI userinfo: scheme://user:pass@host
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^:/\s@]+:)[^@/\s]+@`), "${1}<redacted>@"},
	// DSN userinfo: user:pass@tcp(host)
	{regexp.MustCompile(`\b([A-Za-z0-9_.-]+:)[^@\s/]+@(tcp|unix)\(`), "${1}<redacted>@${2}("},
	// Bearer tokens and well-known key prefixes
	{regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`), "${1}<redacted>"},
	{regexp.MustCompile(`\b(sk|ak|AKIA)[-_A-Za-z0-9]{16,}`), "<redacted-key>"},
}

// Secrets strips credentials from arbitrary text.
func Secrets(s string) string {
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}
