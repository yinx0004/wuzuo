// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"sort"
	"strings"
)

// Wire protocols WuZuo speaks.
const (
	KindAnthropic = "anthropic"
	KindOpenAI    = "openai"
)

// Preset describes a provider WuZuo can detect from the environment.
type Preset struct {
	Name         string
	Kind         string
	KeyEnvs      []string // first non-empty wins
	BaseURL      string
	BaseURLEnv   string
	DefaultModel string // empty means the user must choose one
	ModelEnv     string
}

// Presets in detection order.
var Presets = []Preset{
	{Name: "anthropic", Kind: KindAnthropic, KeyEnvs: []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}, BaseURLEnv: "ANTHROPIC_BASE_URL", DefaultModel: "claude-opus-5-5"},
	{Name: "openai", Kind: KindOpenAI, KeyEnvs: []string{"OPENAI_API_KEY"}, BaseURLEnv: "OPENAI_BASE_URL", ModelEnv: "OPENAI_MODEL"},
	{Name: "deepseek", Kind: KindOpenAI, KeyEnvs: []string{"DEEPSEEK_API_KEY"}, BaseURL: "https://api.deepseek.com", DefaultModel: "deepseek-chat"},
	{Name: "qwen", Kind: KindOpenAI, KeyEnvs: []string{"DASHSCOPE_API_KEY"}, BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", DefaultModel: "qwen-plus"},
	{Name: "ark", Kind: KindOpenAI, KeyEnvs: []string{"ARK_API_KEY"}, BaseURL: "https://ark.cn-beijing.volces.com/api/v3", ModelEnv: "ARK_MODEL"},
}

// BuiltinModel is a short name for a provider + model pair.
type BuiltinModel struct {
	Name, Provider, Model, Description string
}

// BuiltinModels are the short names WuZuo ships with. They follow the current
// generation of each family and are updated with releases; cases record the
// full model ID they resolved to.
var BuiltinModels = []BuiltinModel{
	{Name: "opus", Provider: "anthropic", Model: "claude-opus-5-5", Description: "Claude Opus, strongest default"},
	{Name: "sonnet", Provider: "anthropic", Model: "claude-sonnet-5-5", Description: "Claude Sonnet, faster and cheaper"},
	{Name: "haiku", Provider: "anthropic", Model: "claude-haiku-5-5", Description: "Claude Haiku, fastest"},
	{Name: "fable", Provider: "anthropic", Model: "claude-fable-5-1", Description: "Claude Fable, most capable"},
	{Name: "deepseek", Provider: "deepseek", Model: "deepseek-chat", Description: "DeepSeek chat"},
	{Name: "deepseek-r", Provider: "deepseek", Model: "deepseek-reasoner", Description: "DeepSeek reasoner"},
	{Name: "qwen", Provider: "qwen", Model: "qwen-plus", Description: "Qwen Plus"},
	{Name: "qwen-max", Provider: "qwen", Model: "qwen-max", Description: "Qwen Max"},
}

// ModelAlias is a user-defined short name under `models:` in the config.
type ModelAlias struct {
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model"`
	BaseURL   string `yaml:"base_url,omitempty"`
	APIKeyEnv string `yaml:"api_key_env,omitempty"`
}

// ResolvedLLM is a fully determined provider configuration.
type ResolvedLLM struct {
	// Name is what the user chose (alias, provider/model or raw ID), if anything.
	Name    string
	Preset  string
	Kind    string
	Model   string
	BaseURL string
	// APIKey may be empty for anthropic, in which case the SDK's own credential
	// chain (profiles, workload identity) is used.
	APIKey          string
	Effort          string
	RefusalFallback bool
	// Source explains how the model was chosen, shown in diagnostics.
	Source string
}

// ModelChosen reports whether the user has expressed any model or provider
// preference: a -m override, WUZUO_MODEL/WUZUO_PROVIDER, or llm.model/provider.
func (c *Config) ModelChosen(override string, getenv func(string) string) bool {
	return override != "" || getenv("WUZUO_MODEL") != "" || getenv("WUZUO_PROVIDER") != "" ||
		c.LLM.Model != "" || c.LLM.Provider != ""
}

// ResolveModel determines provider, model, endpoint and key.
//
// The model name comes from the first of: override (-m), $WUZUO_MODEL,
// llm.model. It may be a config alias, a built-in alias, "provider/model",
// a bare provider name, or a raw model ID. Aliases and provider/model names
// carry their provider; otherwise the provider comes from $WUZUO_PROVIDER,
// llm.provider, or the environment, in that order.
func (c *Config) ResolveModel(override string, getenv func(string) string) (*ResolvedLLM, error) {
	name := firstNonEmpty(override, getenv("WUZUO_MODEL"), c.LLM.Model)
	provider := strings.ToLower(firstNonEmpty(getenv("WUZUO_PROVIDER"), c.LLM.Provider))
	model, baseURL, apiKeyEnv := "", "", ""
	source := ""

	switch {
	case name == "":
	case c.Models[name] != (ModelAlias{}):
		a := c.Models[name]
		provider, model, baseURL, apiKeyEnv = strings.ToLower(a.Provider), a.Model, a.BaseURL, a.APIKeyEnv
		source = fmt.Sprintf("alias %q from config", name)
	case builtin(name) != nil:
		b := builtin(name)
		provider, model = b.Provider, b.Model
		source = fmt.Sprintf("built-in alias %q", name)
	case preset(name) != nil:
		provider = name
		source = fmt.Sprintf("provider %q", name)
	default:
		if p, m, ok := strings.Cut(name, "/"); ok && preset(strings.ToLower(p)) != nil {
			provider, model = strings.ToLower(p), m
		} else {
			model = name // raw model ID for the configured or detected provider
		}
		source = fmt.Sprintf("model %q", name)
	}

	if provider == "" {
		for i := range Presets {
			if key := firstEnv(getenv, Presets[i].KeyEnvs); key != "" {
				provider = Presets[i].Name
				detected := "detected $" + keyEnvName(getenv, Presets[i].KeyEnvs)
				source = strings.TrimPrefix(source+", "+detected, ", ")
				break
			}
		}
		if provider == "" {
			return nil, fmt.Errorf("no LLM provider configured: set one of %s, or run `wuzuo init`", allKeyEnvs())
		}
	}
	p := preset(provider)
	if p == nil {
		return nil, fmt.Errorf("unknown llm provider %q (supported: %s)", provider, presetNames())
	}
	if source == "" {
		source = "configured provider"
	}

	// llm.base_url and llm.api_key_env belong to llm.provider; do not carry
	// them over to a different provider chosen through an alias.
	if c.LLM.Provider == "" || strings.EqualFold(c.LLM.Provider, provider) {
		baseURL = firstNonEmpty(baseURL, c.LLM.BaseURL)
		apiKeyEnv = firstNonEmpty(apiKeyEnv, c.LLM.APIKeyEnv)
	}

	r := &ResolvedLLM{
		Name:            name,
		Preset:          p.Name,
		Kind:            p.Kind,
		Effort:          c.LLM.Effort,
		RefusalFallback: c.LLM.RefusalFallback == nil || *c.LLM.RefusalFallback,
		Source:          source,
	}
	if apiKeyEnv != "" {
		r.APIKey = getenv(apiKeyEnv)
		if r.APIKey == "" {
			return nil, fmt.Errorf("api_key_env: environment variable %s is not set", apiKeyEnv)
		}
	} else {
		r.APIKey = firstEnv(getenv, p.KeyEnvs)
	}
	if r.APIKey == "" && p.Kind != KindAnthropic {
		return nil, fmt.Errorf("provider %s: set %s", p.Name, strings.Join(p.KeyEnvs, " or "))
	}

	r.BaseURL = baseURL
	if r.BaseURL == "" && p.BaseURLEnv != "" {
		r.BaseURL = getenv(p.BaseURLEnv)
	}
	if r.BaseURL == "" {
		r.BaseURL = p.BaseURL
	}

	r.Model = model
	if r.Model == "" && p.ModelEnv != "" {
		r.Model = getenv(p.ModelEnv)
	}
	if r.Model == "" {
		r.Model = p.DefaultModel
	}
	if r.Model == "" {
		hint := "-m <provider>/<model>, WUZUO_MODEL"
		if p.ModelEnv != "" {
			hint += " or " + p.ModelEnv
		}
		return nil, fmt.Errorf("provider %s: no model selected; set llm.model, %s", p.Name, hint)
	}
	return r, nil
}

// Choice is a model the user can pick by name.
type Choice struct {
	Name        string
	Provider    string
	Model       string
	Description string
	// Source is "config", "builtin" or "env".
	Source string
	// KeyEnv is the variable that must hold the API key.
	KeyEnv    string
	Available bool
}

// Choices lists config aliases, built-in aliases, and models named by
// provider environment variables (OPENAI_MODEL, ARK_MODEL).
func (c *Config) Choices(getenv func(string) string) []Choice {
	var out []Choice
	names := make([]string, 0, len(c.Models))
	for n := range c.Models {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		a := c.Models[n]
		ch := Choice{Name: n, Provider: strings.ToLower(a.Provider), Model: a.Model, Source: "config"}
		if a.BaseURL != "" {
			ch.Description = a.BaseURL
		}
		if a.APIKeyEnv != "" {
			ch.KeyEnv, ch.Available = a.APIKeyEnv, getenv(a.APIKeyEnv) != ""
		} else {
			ch.KeyEnv, ch.Available = keyStatus(ch.Provider, getenv)
		}
		out = append(out, ch)
	}
	for _, b := range BuiltinModels {
		if _, shadowed := c.Models[b.Name]; shadowed {
			continue
		}
		ch := Choice{Name: b.Name, Provider: b.Provider, Model: b.Model, Description: b.Description, Source: "builtin"}
		ch.KeyEnv, ch.Available = keyStatus(b.Provider, getenv)
		out = append(out, ch)
	}
	for _, p := range Presets {
		if p.ModelEnv == "" {
			continue
		}
		if _, shadowed := c.Models[p.Name]; shadowed {
			continue
		}
		ch := Choice{Name: p.Name, Provider: p.Name, Model: getenv(p.ModelEnv), Source: "env", Description: "from $" + p.ModelEnv}
		ch.KeyEnv, ch.Available = keyStatus(p.Name, getenv)
		if ch.Model == "" {
			ch.Available = false
			ch.Description = "set $" + p.ModelEnv
		}
		out = append(out, ch)
	}
	return out
}

// DefaultChoice is the model WuZuo picks when the user has not chosen one:
// the default model of the first provider (in detection order) whose key is
// set, falling back to any available choice.
func (c *Config) DefaultChoice(getenv func(string) string) (Choice, bool) {
	choices := c.Choices(getenv)
	for _, p := range Presets {
		for _, ch := range choices {
			if ch.Available && ch.Provider == p.Name && (ch.Source == "env" || ch.Model == p.DefaultModel) {
				return ch, true
			}
		}
	}
	for _, ch := range choices {
		if ch.Available {
			return ch, true
		}
	}
	return Choice{}, false
}

func keyStatus(provider string, getenv func(string) string) (string, bool) {
	p := preset(provider)
	if p == nil {
		return "", false
	}
	if n := keyEnvName(getenv, p.KeyEnvs); n != "" {
		return n, true
	}
	return p.KeyEnvs[0], false
}

func builtin(name string) *BuiltinModel {
	for i := range BuiltinModels {
		if BuiltinModels[i].Name == name {
			return &BuiltinModels[i]
		}
	}
	return nil
}

func preset(name string) *Preset {
	for i := range Presets {
		if Presets[i].Name == name {
			return &Presets[i]
		}
	}
	return nil
}

func firstEnv(getenv func(string) string, names []string) string {
	for _, n := range names {
		if v := getenv(n); v != "" {
			return v
		}
	}
	return ""
}

func keyEnvName(getenv func(string) string, names []string) string {
	for _, n := range names {
		if getenv(n) != "" {
			return n
		}
	}
	return ""
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func presetNames() string {
	var n []string
	for _, p := range Presets {
		n = append(n, p.Name)
	}
	return strings.Join(n, ", ")
}

func allKeyEnvs() string {
	var n []string
	for _, p := range Presets {
		n = append(n, p.KeyEnvs[0])
	}
	return strings.Join(n, ", ")
}
