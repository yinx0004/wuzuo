// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Docs that mention built-in short names together with model IDs.
var modelDocs = []string{"README.md", "README.zh-CN.md", "docs/configuration.md", "docs/configuration.zh-CN.md"}

// Places where a doc pairs a short name with a model ID. Each pattern captures
// the short name and the model ID, in that order or reversed (see mention).
var mentionPatterns = []struct {
	re       *regexp.Regexp
	reversed bool // model ID comes first
}{
	{re: regexp.MustCompile("^\\| `([a-z0-9-]+)` \\| [a-z]+ \\| `([^`]+)` \\|$")},               // | `opus` | anthropic | `claude-opus-5-5` |
	{re: regexp.MustCompile(`^\s+\d+\)\s+([a-z0-9-]+)\s+[a-z]+/(\S+)`)},                         //   1)  opus  anthropic/claude-opus-5-5
	{re: regexp.MustCompile(`\S+/(\S+) \(built-in alias "([a-z0-9-]+)"\)`), reversed: true},     // anthropic/claude-opus-5-5 (built-in alias "opus")
	{re: regexp.MustCompile(`^\s+(\S+)\s+\d{4}-\d{2}-\d{2}\s+← ([a-z0-9-]+)$`), reversed: true}, // claude-opus-5-5  2026-09-10  ← opus
	{re: regexp.MustCompile(`"([a-z0-9-]+)" → (\S+) is not in this list`)},                      // "fable" → claude-fable-5-1
	{re: regexp.MustCompile(`newer than "([a-z0-9-]+)" \((\S+)\)`)},                             // newer than "opus" (claude-opus-5-5)
}

// TestDocsListBuiltinModels keeps the docs in step with BuiltinModels: every
// place a doc pairs a built-in short name with a model ID must use the ID the
// code uses, and the configuration docs must list exactly the built-in names.
func TestDocsListBuiltinModels(t *testing.T) {
	want := map[string]string{}
	for _, b := range BuiltinModels {
		want[b.Name] = b.Model
	}
	for _, doc := range modelDocs {
		data, err := os.ReadFile(filepath.Join("..", "..", doc))
		if err != nil {
			t.Fatal(err)
		}
		tableNames := map[string]bool{}
		for n, line := range strings.Split(string(data), "\n") {
			for i, p := range mentionPatterns {
				m := p.re.FindStringSubmatch(line)
				if m == nil {
					continue
				}
				name, model := m[1], m[2]
				if p.reversed {
					name, model = m[2], m[1]
				}
				id, builtin := want[name]
				if !builtin {
					continue // a user alias in an example
				}
				if i == 0 {
					tableNames[name] = true
				}
				if model != id {
					t.Errorf("%s:%d: %q is shown as %s, but the code maps it to %s\n  %s", doc, n+1, name, model, id, strings.TrimSpace(line))
				}
			}
		}
		if strings.HasPrefix(doc, "docs/configuration") {
			var missing []string
			for name := range want {
				if !tableNames[name] {
					missing = append(missing, name)
				}
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				t.Errorf("%s: built-in names missing from the short-name table: %s", doc, strings.Join(missing, ", "))
			}
		}
	}
}

func TestBuiltinModelTable(t *testing.T) {
	seen := map[string]bool{}
	for _, b := range BuiltinModels {
		if seen[b.Name] {
			t.Errorf("duplicate built-in name %q", b.Name)
		}
		seen[b.Name] = true
		if preset(b.Provider) == nil || b.Model == "" || b.Description == "" {
			t.Errorf("incomplete built-in model %+v", b)
		}
		if preset(b.Name) != nil && b.Provider != b.Name {
			t.Errorf("built-in %q shadows a different provider's name", b.Name)
		}
		if b.RefusalFallback && b.Provider != "anthropic" {
			t.Errorf("%q: refusal fallback is an Anthropic API feature", b.Name)
		}
		if b.AdaptiveThinking && b.Provider != "anthropic" {
			t.Errorf("%q: adaptive thinking is an Anthropic API feature", b.Name)
		}
	}
	for _, p := range Presets {
		if p.DefaultAlias == "" {
			continue
		}
		if b := builtin(p.DefaultAlias); b == nil || b.Provider != p.Name {
			t.Errorf("preset %s: default alias %q is not a built-in model of that provider", p.Name, p.DefaultAlias)
		}
	}
}
