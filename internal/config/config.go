// SPDX-License-Identifier: Apache-2.0

// Package config loads WuZuo's configuration file and resolves values that
// may come from the environment (LLM credentials, target DSNs).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root of ~/.wuzuo/config.yaml.
type Config struct {
	LLM LLM `yaml:"llm"`
	// Models defines short names for provider + model pairs, usable with -m,
	// $WUZUO_MODEL and llm.model. They take precedence over built-in names.
	Models   map[string]ModelAlias `yaml:"models"`
	Targets  []Target              `yaml:"targets"`
	Safety   Safety                `yaml:"safety"`
	CasesDir string                `yaml:"cases_dir"`
}

// LLM selects the model provider. Every field is optional: when Provider is
// empty WuZuo detects one from well-known environment variables.
type LLM struct {
	// Provider is one of: anthropic, openai, deepseek, qwen, ark.
	// deepseek/qwen/ark are presets of the OpenAI-compatible provider.
	Provider string `yaml:"provider"`
	// Model is a short name (see `wuzuo models`), "provider/model", or a
	// model ID for Provider.
	Model   string `yaml:"model"`
	BaseURL string `yaml:"base_url"`
	// APIKeyEnv names the environment variable holding the API key. Keys are
	// never read from the config file itself.
	APIKeyEnv string `yaml:"api_key_env"`
	// Effort controls reasoning depth on providers that support it
	// (anthropic: low|medium|high|xhigh|max).
	Effort string `yaml:"effort"`
	// MaxSteps bounds the number of model turns per investigation.
	MaxSteps int `yaml:"max_steps"`
	// RefusalFallback lets the Anthropic API re-serve a declined request with a
	// fallback model instead of failing the investigation.
	RefusalFallback *bool `yaml:"refusal_fallback"`
}

// Target is a system WuZuo may examine.
type Target struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"` // mysql (mongodb coming next)
	Description string `yaml:"description"`
	// DSNEnv names the environment variable holding the connection string.
	DSNEnv string `yaml:"dsn_env"`
	// DSN is accepted for local labs but discouraged: it puts credentials in a file.
	DSN string `yaml:"dsn"`
}

// Safety holds guard rails applied to every toolset.
type Safety struct {
	// Redact normalizes SQL literals and strips secrets before evidence is
	// shown to the model. Defaults to true.
	Redact *bool `yaml:"redact"`
	// QueryTimeout caps each diagnostic query.
	QueryTimeout Duration `yaml:"query_timeout"`
	// MaxEvidenceChars caps how much of one evidence item the model sees.
	MaxEvidenceChars int `yaml:"max_evidence_chars"`
	// AllowExplain enables mysql_explain. The model writes the statement it
	// explains, so it is off by default; it never executes the statement or
	// returns rows.
	AllowExplain bool `yaml:"allow_explain"`
	// AllowRawSQL enables the guarded free-form read-only query tool.
	AllowRawSQL bool `yaml:"allow_raw_sql"`
}

// Duration is a time.Duration that unmarshals from strings like "10s".
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// Std returns the value as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// Defaults.
const (
	DefaultMaxSteps         = 40
	DefaultQueryTimeout     = 10 * time.Second
	DefaultMaxEvidenceChars = 12000
	DefaultEffort           = "high"
)

// Home returns WuZuo's state directory (~/.wuzuo, or $WUZUO_HOME).
func Home() string {
	if h := os.Getenv("WUZUO_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".wuzuo"
	}
	return filepath.Join(home, ".wuzuo")
}

// DefaultPath is where the config file is looked up when none is given.
func DefaultPath() string {
	if p := os.Getenv("WUZUO_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(Home(), "config.yaml")
}

// Load reads the config at path. A missing file at the default location is
// not an error: WuZuo can run purely from environment variables.
func Load(path string) (*Config, error) {
	explicit := path != ""
	if !explicit {
		path = DefaultPath()
	}
	cfg := &Config{}
	data, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec // the operator chooses the config path
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist) && !explicit:
		// fall through with defaults
	default:
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg.applyDefaults()
	cfg.addEnvTargets(os.Getenv)
	return cfg, cfg.Validate()
}

func (c *Config) applyDefaults() {
	if c.LLM.MaxSteps <= 0 {
		c.LLM.MaxSteps = DefaultMaxSteps
	}
	if c.LLM.Effort == "" {
		c.LLM.Effort = DefaultEffort
	}
	if c.LLM.RefusalFallback == nil {
		t := true
		c.LLM.RefusalFallback = &t
	}
	if c.Safety.Redact == nil {
		t := true
		c.Safety.Redact = &t
	}
	if c.Safety.QueryTimeout <= 0 {
		c.Safety.QueryTimeout = Duration(DefaultQueryTimeout)
	}
	if c.Safety.MaxEvidenceChars <= 0 {
		c.Safety.MaxEvidenceChars = DefaultMaxEvidenceChars
	}
	if c.CasesDir == "" {
		c.CasesDir = filepath.Join(Home(), "cases")
	}
	c.CasesDir = expandHome(c.CasesDir)
}

// addEnvTargets registers zero-config targets such as WUZUO_MYSQL_DSN when the
// config file does not already define a target of that type.
func (c *Config) addEnvTargets(getenv func(string) string) {
	envTargets := []struct{ typ, env string }{
		{"mysql", "WUZUO_MYSQL_DSN"},
	}
	for _, et := range envTargets {
		if getenv(et.env) == "" || c.hasType(et.typ) {
			continue
		}
		c.Targets = append(c.Targets, Target{Name: et.typ, Type: et.typ, DSNEnv: et.env, Description: "from $" + et.env})
	}
}

func (c *Config) hasType(t string) bool {
	for _, tg := range c.Targets {
		if tg.Type == t {
			return true
		}
	}
	return false
}

// SupportedTargetTypes lists target types with a built-in toolset.
var SupportedTargetTypes = []string{"mysql"}

// Validate checks structural problems early so they are reported before an
// investigation starts.
func (c *Config) Validate() error {
	seen := map[string]bool{}
	for i, t := range c.Targets {
		if t.Name == "" {
			return fmt.Errorf("targets[%d]: name is required", i)
		}
		if seen[t.Name] {
			return fmt.Errorf("targets: duplicate name %q", t.Name)
		}
		seen[t.Name] = true
		if !contains(SupportedTargetTypes, t.Type) {
			return fmt.Errorf("target %q: unsupported type %q (supported: %s)", t.Name, t.Type, strings.Join(SupportedTargetTypes, ", "))
		}
		if t.DSN == "" && t.DSNEnv == "" {
			return fmt.Errorf("target %q: set dsn_env (recommended) or dsn", t.Name)
		}
	}
	for name, a := range c.Models {
		if name == "" || strings.ContainsAny(name, "/ ") {
			return fmt.Errorf("models: invalid name %q (no spaces or slashes)", name)
		}
		if preset(strings.ToLower(a.Provider)) == nil {
			return fmt.Errorf("models.%s: unknown provider %q (supported: %s)", name, a.Provider, presetNames())
		}
		if a.Model == "" {
			return fmt.Errorf("models.%s: model is required", name)
		}
	}
	switch c.LLM.Effort {
	case "low", "medium", "high", "xhigh", "max":
	default:
		return fmt.Errorf("llm.effort: unsupported value %q", c.LLM.Effort)
	}
	return nil
}

// ResolveDSN returns the connection string for a target.
func (t Target) ResolveDSN() (string, error) {
	if t.DSNEnv != "" {
		v := os.Getenv(t.DSNEnv)
		if v == "" {
			return "", fmt.Errorf("target %q: environment variable %s is not set", t.Name, t.DSNEnv)
		}
		return v, nil
	}
	return t.DSN, nil
}

// SelectTargets returns the named targets, or all targets if names is empty.
func (c *Config) SelectTargets(names []string) ([]Target, error) {
	if len(names) == 0 {
		return c.Targets, nil
	}
	var out []Target
	for _, n := range names {
		found := false
		for _, t := range c.Targets {
			if t.Name == n {
				out = append(out, t)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown target %q (configured: %s)", n, strings.Join(c.TargetNames(), ", "))
		}
	}
	return out, nil
}

// TargetNames lists configured target names.
func (c *Config) TargetNames() []string {
	names := make([]string, len(c.Targets))
	for i, t := range c.Targets {
		names[i] = t.Name
	}
	return names
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
