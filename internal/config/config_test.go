// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolveModel(t *testing.T) {
	aliases := map[string]ModelAlias{
		"doubao": {Provider: "ark", Model: "ep-123"},
		"local":  {Provider: "openai", Model: "qwen3:32b", BaseURL: "http://localhost:11434/v1", APIKeyEnv: "OLLAMA_KEY"},
		"sonnet": {Provider: "anthropic", Model: "claude-sonnet-pinned"}, // shadows the built-in
	}
	cases := []struct {
		name      string
		env       map[string]string
		llm       LLM
		override  string
		wantKind  string
		wantModel string
		wantURL   string
		wantErr   bool
	}{
		{name: "detect anthropic first", env: map[string]string{"ANTHROPIC_API_KEY": "k", "OPENAI_API_KEY": "o"}, wantKind: KindAnthropic, wantModel: id("opus")},
		{name: "deepseek preset", env: map[string]string{"DEEPSEEK_API_KEY": "k"}, wantKind: KindOpenAI, wantModel: id("deepseek"), wantURL: "https://api.deepseek.com"},
		{name: "openai needs model", env: map[string]string{"OPENAI_API_KEY": "k"}, wantErr: true},
		{name: "openai compatible from env", env: map[string]string{"OPENAI_API_KEY": "k", "OPENAI_BASE_URL": "http://localhost:11434/v1", "OPENAI_MODEL": "qwen3"}, wantKind: KindOpenAI, wantModel: "qwen3", wantURL: "http://localhost:11434/v1"},
		{name: "built-in alias picks its provider", env: map[string]string{"ANTHROPIC_API_KEY": "k", "DEEPSEEK_API_KEY": "d"}, override: "deepseek-r", wantKind: KindOpenAI, wantModel: id("deepseek-r"), wantURL: "https://api.deepseek.com"},
		{name: "config alias shadows built-in", env: map[string]string{"ANTHROPIC_API_KEY": "k"}, override: "sonnet", wantKind: KindAnthropic, wantModel: "claude-sonnet-pinned"},
		{name: "config alias with own endpoint and key", env: map[string]string{"OLLAMA_KEY": "x"}, override: "local", wantKind: KindOpenAI, wantModel: "qwen3:32b", wantURL: "http://localhost:11434/v1"},
		{name: "config alias for ark", env: map[string]string{"ARK_API_KEY": "a"}, llm: LLM{Model: "doubao"}, wantKind: KindOpenAI, wantModel: "ep-123", wantURL: "https://ark.cn-beijing.volces.com/api/v3"},
		{name: "provider/model", env: map[string]string{"OPENAI_API_KEY": "k"}, override: "openai/gpt-5", wantKind: KindOpenAI, wantModel: "gpt-5"},
		{name: "bare provider name", env: map[string]string{"ARK_API_KEY": "a", "ARK_MODEL": "ep-9"}, override: "ark", wantKind: KindOpenAI, wantModel: "ep-9", wantURL: "https://ark.cn-beijing.volces.com/api/v3"},
		{name: "-m beats WUZUO_MODEL beats llm.model", env: map[string]string{"ANTHROPIC_API_KEY": "k", "WUZUO_MODEL": "haiku"}, llm: LLM{Model: "opus"}, override: "sonnet", wantKind: KindAnthropic, wantModel: "claude-sonnet-pinned"},
		{name: "WUZUO_MODEL beats llm.model", env: map[string]string{"ANTHROPIC_API_KEY": "k", "WUZUO_MODEL": "haiku"}, llm: LLM{Model: "opus"}, wantKind: KindAnthropic, wantModel: id("haiku")},
		{name: "raw ID uses configured provider", env: map[string]string{"ARK_API_KEY": "a"}, llm: LLM{Provider: "ark", Model: "ep-777"}, wantKind: KindOpenAI, wantModel: "ep-777", wantURL: "https://ark.cn-beijing.volces.com/api/v3"},
		{name: "ollama tag with colon is a raw ID", env: map[string]string{"OPENAI_API_KEY": "k"}, llm: LLM{Provider: "openai", BaseURL: "http://h/v1"}, override: "qwen3:32b", wantKind: KindOpenAI, wantModel: "qwen3:32b", wantURL: "http://h/v1"},
		{name: "llm.base_url does not leak into another provider", env: map[string]string{"ANTHROPIC_API_KEY": "k", "OPENAI_API_KEY": "o"}, llm: LLM{Provider: "openai", Model: "m", BaseURL: "http://gateway/v1"}, override: "opus", wantKind: KindAnthropic, wantModel: id("opus")},
		{name: "anthropic without key but with federation uses SDK chain", env: map[string]string{"ANTHROPIC_FEDERATION_RULE_ID": "r"}, llm: LLM{Provider: "anthropic"}, wantKind: KindAnthropic, wantModel: id("opus")},
		{name: "anthropic alias without any credentials", env: map[string]string{}, llm: LLM{Model: "opus"}, wantErr: true},
		{name: "alias whose key is missing", env: map[string]string{}, override: "deepseek", wantErr: true},
		{name: "nothing configured", env: map[string]string{}, wantErr: true},
		{name: "unknown provider", env: map[string]string{}, llm: LLM{Provider: "nope"}, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{LLM: c.llm, Models: aliases}
			r, err := cfg.ResolveModel(c.override, env(c.env))
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", r)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.Kind != c.wantKind || r.Model != c.wantModel || r.BaseURL != c.wantURL {
				t.Fatalf("got kind=%s model=%s url=%s (%s)", r.Kind, r.Model, r.BaseURL, r.Source)
			}
		})
	}
}

func TestChoicesAndDefault(t *testing.T) {
	cfg := &Config{Models: map[string]ModelAlias{"doubao": {Provider: "ark", Model: "ep-1"}}}
	e := env(map[string]string{"DEEPSEEK_API_KEY": "d", "ARK_API_KEY": "a"})
	byName := map[string]Choice{}
	for _, ch := range cfg.Choices(e) {
		byName[ch.Name] = ch
	}
	if !byName["doubao"].Available || byName["doubao"].Source != "config" {
		t.Errorf("doubao: %+v", byName["doubao"])
	}
	if byName["opus"].Available || byName["opus"].KeyEnv != "ANTHROPIC_API_KEY" {
		t.Errorf("opus: %+v", byName["opus"])
	}
	if byName["ark"].Available {
		t.Errorf("ark env choice without ARK_MODEL must be unavailable: %+v", byName["ark"])
	}
	def, ok := cfg.DefaultChoice(e)
	if !ok || def.Name != "deepseek" {
		t.Fatalf("default = %+v", def)
	}
	if _, ok := (&Config{}).DefaultChoice(env(map[string]string{})); ok {
		t.Fatal("no keys must mean no default")
	}
}

func TestModelChosen(t *testing.T) {
	none := env(map[string]string{})
	if (&Config{}).ModelChosen("", none) {
		t.Error("empty config should not count as chosen")
	}
	for _, c := range []struct {
		cfg      Config
		override string
		env      map[string]string
	}{
		{override: "opus"},
		{env: map[string]string{"WUZUO_MODEL": "opus"}},
		{env: map[string]string{"WUZUO_PROVIDER": "deepseek"}},
		{cfg: Config{LLM: LLM{Model: "opus"}}},
		{cfg: Config{LLM: LLM{Provider: "ark"}}},
	} {
		if !c.cfg.ModelChosen(c.override, env(c.env)) {
			t.Errorf("should count as chosen: %+v", c)
		}
	}
}

func TestLoadDefaultsAndEnvTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("safety:\n  query_timeout: 3s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WUZUO_MYSQL_DSN", "u@tcp(localhost:3306)/")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Safety.QueryTimeout.Std().Seconds() != 3 {
		t.Errorf("query_timeout = %v", cfg.Safety.QueryTimeout.Std())
	}
	if !*cfg.Safety.Redact || cfg.LLM.MaxSteps != DefaultMaxSteps {
		t.Errorf("defaults not applied: %+v", cfg)
	}
	if len(cfg.Targets) != 1 || cfg.Targets[0].Name != "mysql" {
		t.Fatalf("env target not added: %+v", cfg.Targets)
	}
	dsn, err := cfg.Targets[0].ResolveDSN()
	if err != nil || dsn == "" {
		t.Fatalf("ResolveDSN: %q %v", dsn, err)
	}
}

func TestValidateRejectsBadTargets(t *testing.T) {
	c := &Config{Targets: []Target{{Name: "a", Type: "oracle", DSN: "x"}}}
	c.applyDefaults()
	if err := c.Validate(); err == nil {
		t.Fatal("expected unsupported type error")
	}
}

func TestModelSQLSwitchesDefaultOff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("safety:\n  allow_explain: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Safety.AllowExplain || cfg.Safety.AllowRawSQL {
		t.Fatalf("allow_explain=%v allow_raw_sql=%v", cfg.Safety.AllowExplain, cfg.Safety.AllowRawSQL)
	}
	empty := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(empty, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(empty)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Safety.AllowExplain || cfg.Safety.AllowRawSQL {
		t.Fatal("model-SQL probes must be off by default")
	}
}

func TestValidateModels(t *testing.T) {
	for _, m := range []map[string]ModelAlias{
		{"x": {Provider: "nope", Model: "m"}},
		{"x": {Provider: "ark"}},
		{"a/b": {Provider: "ark", Model: "m"}},
	} {
		c := &Config{Models: m}
		c.applyDefaults()
		if err := c.Validate(); err == nil {
			t.Errorf("expected error for %v", m)
		}
	}
}

func TestMissingKeyMessageNamesModelAndVariable(t *testing.T) {
	_, err := (&Config{LLM: LLM{Model: "sonnet"}}).ResolveModel("", env(map[string]string{}))
	if err == nil || !strings.Contains(err.Error(), `model "sonnet" (anthropic) needs an API key: export ANTHROPIC_API_KEY=`) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveEndpointWithoutModel(t *testing.T) {
	cfg := &Config{Models: map[string]ModelAlias{"local": {Provider: "openai", Model: "qwen3", BaseURL: "http://h:11434/v1", APIKeyEnv: "OLLAMA_KEY"}}}
	e := env(map[string]string{"OPENAI_API_KEY": "k", "OLLAMA_KEY": "o"})
	r, err := cfg.ResolveEndpoint("openai", e)
	if err != nil || r.Kind != KindOpenAI || r.APIKey != "k" {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := cfg.ResolveModel("openai", e); err == nil {
		t.Fatal("ResolveModel must still require a model")
	}
	r, err = cfg.ResolveEndpoint("local", e)
	if err != nil || r.BaseURL != "http://h:11434/v1" || r.APIKey != "o" {
		t.Fatalf("alias endpoint: %+v %v", r, err)
	}
}

func TestRefusalFallbackFollowsModelTable(t *testing.T) {
	e := env(map[string]string{"ANTHROPIC_API_KEY": "k"})
	yes, no := true, false
	cases := []struct {
		model string
		set   *bool
		want  bool
	}{
		{model: "opus", want: true},                        // built-in, supported
		{model: "haiku", want: false},                      // built-in, not supported
		{model: "anthropic/claude-new-model", want: false}, // unknown: off by default
		{model: "anthropic/claude-new-model", set: &yes, want: true},
		{model: "opus", set: &no, want: false},
	}
	for _, c := range cases {
		cfg := &Config{LLM: LLM{RefusalFallback: c.set}}
		r, err := cfg.ResolveModel(c.model, e)
		if err != nil {
			t.Fatal(err)
		}
		if r.RefusalFallback != c.want {
			t.Errorf("model=%s set=%v: RefusalFallback=%v, want %v", c.model, c.set, r.RefusalFallback, c.want)
		}
	}
}

// id returns the model ID a built-in short name currently points to, so tests
// keep passing when a built-in model is updated.
func id(name string) string { return builtin(name).Model }
