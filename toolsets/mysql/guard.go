// SPDX-License-Identifier: Apache-2.0

package mysql

import (
	"fmt"
	"regexp"
	"strings"
)

// The guard is one layer of defense for free-form queries. The others are a
// read-only session (transaction_read_only=ON), max_execution_time, a row cap,
// and — most importantly — a database account that only has read privileges.

var (
	leadingWord = regexp.MustCompile(`^\s*([A-Za-z]+)`)
	explainAnal = regexp.MustCompile(`(?i)^\s*EXPLAIN\s+ANALYZE\b`)
	modifyingKw = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|REPLACE)\b`)
	forbidden   = regexp.MustCompile(`(?i)\b(INTO\s+(OUT|DUMP)FILE|INTO\s+@|FOR\s+UPDATE|FOR\s+SHARE|LOCK\s+IN\s+SHARE\s+MODE|SLEEP\s*\(|BENCHMARK\s*\(|GET_LOCK\s*\(|LOAD_FILE\s*\(|RELEASE_LOCK\s*\(|RELEASE_ALL_LOCKS\s*\()`)
)

var allowedLeading = map[string]bool{
	"SELECT": true, "WITH": true, "SHOW": true, "EXPLAIN": true, "DESC": true, "DESCRIBE": true, "TABLE": true,
}

// CheckReadOnly rejects anything that is not a single read-only statement.
func CheckReadOnly(sql string) error {
	_, err := GuardReadOnly(sql)
	return err
}

// GuardReadOnly validates sql and returns the exact text to execute (trimmed,
// without a trailing semicolon), so what was checked is what runs.
func GuardReadOnly(sql string) (string, error) {
	stmt := strings.TrimSpace(sql)
	stmt = strings.TrimSpace(strings.TrimSuffix(stmt, ";"))
	if err := checkSkeleton(stmt); err != nil {
		return "", err
	}
	return stmt, nil
}

func checkSkeleton(sql string) error {
	s, err := skeleton(sql)
	if err != nil {
		return err
	}
	s = strings.TrimSpace(s)
	s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
	if s == "" {
		return fmt.Errorf("empty statement")
	}
	if strings.Contains(s, ";") {
		return fmt.Errorf("only a single statement is allowed")
	}
	m := leadingWord.FindStringSubmatch(s)
	if m == nil || !allowedLeading[strings.ToUpper(m[1])] {
		return fmt.Errorf("only SELECT, WITH, SHOW, EXPLAIN, DESCRIBE and TABLE statements are allowed")
	}
	upper := strings.ToUpper(m[1])
	if upper == "EXPLAIN" && explainAnal.MatchString(s) {
		return fmt.Errorf("EXPLAIN ANALYZE executes the statement and is not allowed")
	}
	if loc := forbidden.FindString(s); loc != "" {
		return fmt.Errorf("%q is not allowed in diagnostic queries", strings.TrimSpace(loc))
	}
	if upper == "WITH" && modifyingKw.MatchString(s) {
		return fmt.Errorf("data-modifying CTEs are not allowed")
	}
	if strings.Contains(s, ":=") {
		return fmt.Errorf("variable assignment is not allowed")
	}
	return nil
}

// skeleton lexes the statement once, replacing comments with a space and the
// contents of quoted strings with spaces, so that checks only ever see SQL
// structure. Anything the lexer cannot classify the same way the server would
// is rejected rather than guessed at: executable comments (/*! */) and hints
// (/*+ */), unterminated quotes or comments, and backslashes inside literals.
func skeleton(sql string) (string, error) {
	rs := []rune(sql)
	n := len(rs)
	var b strings.Builder
	for i := 0; i < n; i++ {
		c := rs[i]
		switch {
		case c == '/' && i+1 < n && rs[i+1] == '*':
			if i+2 < n && (rs[i+2] == '!' || rs[i+2] == '+') {
				return "", fmt.Errorf("executable comments and optimizer hints are not allowed")
			}
			end := strings.Index(string(rs[i+2:]), "*/")
			if end < 0 {
				return "", fmt.Errorf("unterminated comment")
			}
			i += 2 + len([]rune(string(rs[i+2:])[:end])) + 1
			b.WriteRune(' ')
		case c == '#' || (c == '-' && i+2 < n && rs[i+1] == '-' && (rs[i+2] == ' ' || rs[i+2] == '\t')) || (c == '-' && i+2 == n && rs[i+1] == '-'):
			for i < n && rs[i] != '\n' {
				i++
			}
			b.WriteRune(' ')
		case c == '\'' || c == '"' || c == '`':
			b.WriteRune(c)
			j := i + 1
			closed := false
			for j < n {
				if rs[j] == '\\' && c != '`' {
					// Whether a backslash escapes depends on the server's
					// sql_mode (NO_BACKSLASH_ESCAPES), so the lexer could
					// disagree with the server about where the string ends.
					return "", fmt.Errorf("backslashes in string literals are not allowed")
				}
				if rs[j] == c {
					if j+1 < n && rs[j+1] == c {
						b.WriteString("  ")
						j += 2
						continue
					}
					closed = true
					break
				}
				if c == '`' {
					b.WriteRune('x') // keep identifiers word-like but inert
				} else {
					b.WriteRune(' ')
				}
				j++
			}
			if !closed {
				return "", fmt.Errorf("unterminated quote")
			}
			b.WriteRune(c)
			i = j
		default:
			b.WriteRune(c)
		}
	}
	return b.String(), nil
}

var identRe = regexp.MustCompile(`^[A-Za-z0-9_$]{1,64}$`)

// quoteIdent validates and backtick-quotes a schema or table name.
func quoteIdent(s string) (string, error) {
	if !identRe.MatchString(s) {
		return "", fmt.Errorf("invalid identifier %q", s)
	}
	return "`" + s + "`", nil
}
