// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveDefaultModelCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	if err := SaveDefaultModel(path, "sonnet", "anthropic"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Model != "sonnet" {
		t.Fatalf("model = %q", cfg.LLM.Model)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}
}

func TestSaveDefaultModelKeepsExistingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	orig := `# my notes
llm:
  provider: openai # was a gateway
  model: old
  max_steps: 12
targets:
  - name: db1
    type: mysql
    dsn_env: DB1_DSN # keep me
`
	if err := os.WriteFile(path, []byte(orig), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := SaveDefaultModel(path, "deepseek", "deepseek"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	out := string(data)
	for _, want := range []string{"# my notes", "# keep me", "# was a gateway", "max_steps: 12", "model: deepseek", "provider: deepseek"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o640 {
		t.Errorf("mode changed to %v", st.Mode().Perm())
	}
}

func TestSaveDefaultModelAddsLLMSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("safety:\n  redact: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveDefaultModel(path, "opus", "anthropic"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Model != "opus" || cfg.LLM.Provider != "" || !*cfg.Safety.Redact {
		t.Fatalf("cfg = %+v", cfg.LLM)
	}
}
