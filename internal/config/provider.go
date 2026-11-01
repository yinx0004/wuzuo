// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	Name       string
	Kind       string
	KeyEnvs    []string // first non-empty wins
	BaseURL    string
	BaseURLEnv string
	// DefaultAlias names the built-in model used when only the provider is
	// known. Empty means the user must choose a model.
	DefaultAlias string
	ModelEnv     string
}

// Presets in detection order.
var Presets = []Preset{
	{Name: "anthropic", Kind: KindAnthropic, KeyEnvs: []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}, BaseURLEnv: "ANTHROPIC_BASE_URL", DefaultAlias: "opus"},
	{Name: "openai", Kind: KindOpenAI, KeyEnvs: []string{"OPENAI_API_KEY"}, BaseURLEnv: "OPENAI_BASE_URL", ModelEnv: "OPENAI_MODEL"},
	{Name: "deepseek", Kind: KindOpenAI, KeyEnvs: []string{"DEEPSEEK_API_KEY"}, BaseURL: "https://api.deepseek.com", DefaultAlias: "deepseek"},
	{Name: "qwen", Kind: KindOpenAI, KeyEnvs: []string{"DASHSCOPE_API_KEY"}, BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", DefaultAlias: "qwen"},
	{Name: "ark", Kind: KindOpenAI, KeyEnvs: []string{"ARK_API_KEY"}, BaseURL: "https://ark.cn-beijing.volces.com/api/v3", ModelEnv: "ARK_MODEL"},
}

// BuiltinModel is a short name for a provider + model pair, with what WuZuo
// knows about the model.
type BuiltinModel struct {
	Name, Provider, Model, Description string
	// RefusalFallback: the Anthropic API accepts `fallbacks: "default"` for
	// this model, re-serving a declined request on a fallback model.
	RefusalFallback bool
	// AdaptiveThinking: the model accepts `thinking: {type: "adaptive"}`, so
	// WuZuo can ask for summarized thinking to record in the transcript.
	AdaptiveThinking bool
}

// BuiltinModels is the only place in the code that names model IDs. Presets
// refer to it by name, and per-model behaviour is read from its fields. To
// move a short name to a newer model, change its row here and the tables in
// docs/configuration*.md (TestDocsListBuiltinModels checks they match); see
// CONTRIBUTING.md. Cases record the full model ID they resolved to.
var BuiltinModels = []BuiltinModel{
	{Name: "opus", Provider: "anthropic", Model: "claude-opus-5-5", Description: "Claude Opus, strongest default", RefusalFallback: true, AdaptiveThinking: true},
	{Name: "sonnet", Provider: "anthropic", Model: "claude-sonnet-5-5", Description: "Claude Sonnet, faster and cheaper", RefusalFallback: true, AdaptiveThinking: true},
	{Name: "haiku", Provider: "anthropic", Model: "claude-haiku-5-5", Description: "Claude Haiku, fastest", AdaptiveThinking: true},
	{Name: "fable", Provider: "anthropic", Model: "claude-fable-5-1", Description: "Claude Fable, most capable", RefusalFallback: true, AdaptiveThinking: true},
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
	// ThinkingSummary asks the provider for a readable summary of the model's
	// reasoning, recorded in the case transcript.
	ThinkingSummary bool
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
	return c.resolve(firstNonEmpty(override, getenv("WUZUO_MODEL"), c.LLM.Model), true, getenv)
}

// ResolveEndpoint resolves the endpoint and key for name (a provider or a
// model alias) without requiring a model to be chosen. It is used to list the
// models an endpoint offers.
func (c *Config) ResolveEndpoint(name string, getenv func(string) string) (*ResolvedLLM, error) {
	return c.resolve(name, false, getenv)
}

func (c *Config) resolve(name string, requireModel bool, getenv func(string) string) (*ResolvedLLM, error) {
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
			return nil, errors.New(NoKeyHelp())
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
		Name:   name,
		Preset: p.Name,
		Kind:   p.Kind,
		Effort: c.LLM.Effort,
		Source: source,
	}
	if apiKeyEnv != "" {
		r.APIKey = getenv(apiKeyEnv)
		if r.APIKey == "" {
			return nil, fmt.Errorf("api_key_env: environment variable %s is not set", apiKeyEnv)
		}
	} else {
		r.APIKey = firstEnv(getenv, p.KeyEnvs)
	}
	if r.APIKey == "" && (p.Kind != KindAnthropic || !anthropicSDKCredentials(getenv)) {
		what := p.Name
		if name != "" {
			what = fmt.Sprintf("model %q (%s)", name, p.Name)
		}
		return nil, fmt.Errorf("%s needs an API key: export %s=... in your shell (see `wuzuo models`)", what, p.KeyEnvs[0])
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
		r.Model = p.defaultModel()
	}
	r.ThinkingSummary = SupportsAdaptiveThinking(p.Name, r.Model)
	if c.LLM.RefusalFallback != nil {
		r.RefusalFallback = *c.LLM.RefusalFallback
	} else {
		r.RefusalFallback = SupportsRefusalFallback(p.Name, r.Model)
	}
	if r.Model == "" && requireModel {
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
			if ch.Available && ch.Provider == p.Name && (ch.Source == "env" || ch.Model == p.defaultModel()) {
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

// KeyEnvFor returns the key variable of a provider, whether it is set, and
// whether the provider is known at all.
func KeyEnvFor(provider string, getenv func(string) string) (keyEnv string, set, known bool) {
	if preset(provider) == nil {
		return "", false, false
	}
	keyEnv, set = keyStatus(provider, getenv)
	return keyEnv, set, true
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

// defaultModel is the model ID of the preset's default alias, or "".
func (p *Preset) defaultModel() string {
	if b := builtin(p.DefaultAlias); b != nil {
		return b.Model
	}
	return ""
}

// SupportsRefusalFallback reports whether model (a full ID) is a built-in
// model known to accept server-side refusal fallback.
func SupportsRefusalFallback(provider, model string) bool {
	for _, b := range BuiltinModels {
		if b.Provider == provider && b.Model == model {
			return b.RefusalFallback
		}
	}
	return false
}

// SupportsAdaptiveThinking reports whether model (a full ID) is a built-in
// model known to accept adaptive thinking with a display setting.
func SupportsAdaptiveThinking(provider, model string) bool {
	for _, b := range BuiltinModels {
		if b.Provider == provider && b.Model == model {
			return b.AdaptiveThinking
		}
	}
	return false
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

// anthropicSDKCredentials reports whether the Anthropic SDK can find
// credentials without an API key variable: workload identity federation or a
// profile written by `ant auth login`.
func anthropicSDKCredentials(getenv func(string) string) bool {
	if getenv("ANTHROPIC_FEDERATION_RULE_ID") != "" || getenv("ANTHROPIC_PROFILE") != "" {
		return true
	}
	dir := getenv("XDG_CONFIG_HOME")
	if dir == "" && getenv("HOME") != "" {
		dir = filepath.Join(getenv("HOME"), ".config")
	}
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "anthropic"))
	return err == nil
}

// NoKeyHelp explains which environment variables enable which models. It is
// shown when no LLM API key is set.
func NoKeyHelp() string {
	var b strings.Builder
	b.WriteString("no LLM API key found. Set one of these in your shell and run again:\n")
	for _, p := range Presets {
		var names []string
		for _, m := range BuiltinModels {
			if m.Provider == p.Name {
				names = append(names, m.Name)
			}
		}
		what := strings.Join(names, ", ")
		if p.ModelEnv != "" {
			what = fmt.Sprintf("%s models; also set %s, or use -m %s/<model-id>", p.Name, p.ModelEnv, p.Name)
		}
		fmt.Fprintf(&b, "  export %-20s # %s\n", p.KeyEnvs[0]+"=...", what)
	}
	b.WriteString("Then run `wuzuo init` to choose a default model and add targets. See docs/configuration.md.")
	return b.String()
}
