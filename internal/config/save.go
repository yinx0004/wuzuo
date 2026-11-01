// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Template returns a commented starter config with llm.model set to model and
// the given targets (an commented example when there are none).
func Template(model string, targets []Target) string {
	return fmt.Sprintf(`# WuZuo configuration. Reference: docs/configuration.md
# No secrets here: API keys and DSNs live in environment variables or in
# ~/.wuzuo/credentials (written by wuzuo init, mode 0600).

llm:
  # A short name from `+"`wuzuo models`"+` (opus, sonnet, deepseek, qwen, ...), an alias
  # from "models:" below, or "provider/model-id". Override per run with -m.
  model: %q
  # provider: anthropic   # only needed with a raw model ID
  # base_url: http://localhost:11434/v1   # any OpenAI-compatible endpoint
  # api_key_env: MY_LLM_KEY
  effort: high          # anthropic only: low | medium | high | xhigh | max
  max_steps: 40

# Your own short names, e.g. for Doubao endpoints or local models.
# models:
#   doubao: { provider: ark, model: ep-20261101-xxxxx }
#   local:  { provider: openai, model: qwen3:32b, base_url: http://localhost:11434/v1 }

targets:
%s
safety:
  redact: true              # normalize SQL literals, strip secrets and row dumps
  query_timeout: 10s
  max_evidence_chars: 12000
  # Probes where the model writes SQL. Both run read-only and are off by default.
  allow_explain: false      # mysql_explain: plans of SELECTs (never executes them)
  allow_raw_sql: false      # mysql_query: guarded free-form read-only queries
`, model, targetsYAML(targets))
}

func targetsYAML(targets []Target) string {
	if len(targets) == 0 {
		return `  # - name: prod-orders
  #   type: mysql
  #   description: primary of the orders cluster
  #   dsn_env: WUZUO_PROD_ORDERS_DSN   # e.g. wuzuo:***@tcp(10.0.0.5:3306)/
`
	}
	var b bytes.Buffer
	for _, t := range targets {
		fmt.Fprintf(&b, "  - name: %q\n    type: %s\n", t.Name, t.Type)
		if t.Description != "" {
			fmt.Fprintf(&b, "    description: %q\n", t.Description)
		}
		fmt.Fprintf(&b, "    dsn_env: %s   # e.g. wuzuo:***@tcp(10.0.0.5:3306)/\n", t.DSNEnv)
	}
	return b.String()
}

// SaveDefaultModel sets llm.model in the config file at path, creating the
// file from Template if it does not exist. Comments and other settings in an
// existing file are kept. If the file also sets llm.provider, it is updated
// to provider so the two cannot disagree.
func SaveDefaultModel(path, model, provider string) error {
	data, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec // the operator chooses the config path
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(Template(model, nil)), 0o600)
	}
	if err != nil {
		return err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: top level is not a mapping", path)
	}
	llm := mapValue(root, "llm")
	if llm == nil || llm.Kind != yaml.MappingNode {
		llm = &yaml.Node{Kind: yaml.MappingNode}
		setMapValue(root, "llm", llm)
	}
	setMapValue(llm, "model", &yaml.Node{Kind: yaml.ScalarNode, Value: model})
	if p := mapValue(llm, "provider"); p != nil && provider != "" {
		p.Kind, p.Tag, p.Style, p.Value = yaml.ScalarNode, "", 0, provider
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	return os.WriteFile(path, buf.Bytes(), mode)
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func setMapValue(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			// Keep the comments attached to the old value.
			old := m.Content[i+1]
			v.HeadComment, v.LineComment, v.FootComment = old.HeadComment, old.LineComment, old.FootComment
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
}
