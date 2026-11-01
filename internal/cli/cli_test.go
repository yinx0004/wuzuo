// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/yinx0004/yuanfang/internal/config"
	"github.com/yinx0004/yuanfang/internal/llm"
)

type oneShot struct{ calls int }

func (o *oneShot) Name() string  { return "fake" }
func (o *oneShot) Model() string { return "fake-1" }
func (o *oneShot) Chat(context.Context, *llm.Request) (*llm.Response, error) {
	o.calls++
	args := `{"title":"无法判断","summary":"没有可排查的对象。","root_cause":{"statement":"未确认","confidence":"low","evidence":[]},"open_questions":["配置一个目标"]}`
	return &llm.Response{
		Message:    llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "submit_diagnosis", Args: json.RawMessage(args)}}},
		StopReason: llm.StopToolUse,
		Usage:      llm.Usage{InputTokens: 12, CacheReadTokens: 38168, CacheWriteTokens: 500, OutputTokens: 7331},
	}, nil
}

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := newRoot()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

func TestAskPersistsCaseAndReport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("YUANFANG_HOME", home)
	t.Setenv("YUANFANG_CONFIG", filepath.Join(home, "none.yaml"))
	t.Setenv("YUANFANG_MYSQL_DSN", "")
	t.Setenv("YUANFANG_PROVIDER", "anthropic")
	t.Setenv("ANTHROPIC_API_KEY", fakeSecret("api-key"))
	fake := &oneShot{}
	newProvider = func(*config.ResolvedLLM) (llm.Provider, error) { return fake, nil }
	t.Cleanup(func() { newProvider = llm.New })

	out, errOut, err := run(t, "ask", "为什么订单库变慢了")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "tokens: 38680 in (38168 read from cache, 500 written to cache) / 7331 out") {
		t.Errorf("token summary:\n%s", errOut)
	}
	if !strings.Contains(out, "# 无法判断") || !strings.Contains(out, "存疑") {
		t.Fatalf("report:\n%s", out)
	}

	listOut, _, err := run(t, "case", "list")
	if err != nil || !strings.Contains(listOut, "closed") {
		t.Fatalf("case list: %v\n%s", err, listOut)
	}
	id := strings.Fields(strings.Split(listOut, "\n")[1])[0]
	if _, err := os.Stat(filepath.Join(home, "cases", id, "report.json")); err != nil {
		t.Fatal(err)
	}
	showOut, _, err := run(t, "case", "show", id[:15], "--json")
	if err != nil || !strings.Contains(showOut, `"confidence": "low"`) {
		t.Fatalf("case show: %v\n%s", err, showOut)
	}
}

// setupInteractive isolates Yuanfang's home and fakes a terminal whose user
// types answers.
func setupInteractive(t *testing.T, answers string) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("YUANFANG_HOME", home)
	t.Setenv("YUANFANG_CONFIG", "")
	t.Setenv("YUANFANG_MODEL", "")
	t.Setenv("YUANFANG_PROVIDER", "")
	t.Setenv("YUANFANG_MYSQL_DSN", "")
	for _, k := range []string{"ANTHROPIC_AUTH_TOKEN", "OPENAI_API_KEY", "DEEPSEEK_API_KEY", "DASHSCOPE_API_KEY", "ARK_API_KEY"} {
		t.Setenv(k, "")
	}
	t.Setenv("ANTHROPIC_API_KEY", fakeSecret("api-key"))
	t.Setenv("ANTHROPIC_BASE_URL", "")
	interactive = func() bool { return true }
	promptInput = strings.NewReader(answers)
	fake := &oneShot{}
	newProvider = func(*config.ResolvedLLM) (llm.Provider, error) { return fake, nil }
	t.Cleanup(func() {
		interactive = func() bool { return false }
		promptInput = os.Stdin
		newProvider = llm.New
	})
	return home
}

func TestAskPromptsForModelAndSaves(t *testing.T) {
	home := setupInteractive(t, "2\n\n") // pick #2 (sonnet), Enter = save
	_, errOut, err := run(t, "ask", "why slow")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"No model is configured yet.", "opus", "(default)", "deepseek, deepseek-r — set DEEPSEEK_API_KEY", "Created " + filepath.Join(home, "config.yaml") + " with llm.model = sonnet"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("prompt output missing %q:\n%s", want, errOut)
		}
	}
	cfg, err := config.Load(filepath.Join(home, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Model != "sonnet" {
		t.Fatalf("saved model = %q", cfg.LLM.Model)
	}

	// The next run uses the saved model without asking.
	promptInput = strings.NewReader("")
	_, errOut, err = run(t, "ask", "why slow")
	if err != nil || strings.Contains(errOut, "No model is configured") {
		t.Fatalf("prompted again: %v\n%s", err, errOut)
	}
}

func TestAskPromptDefaultAndDecline(t *testing.T) {
	home := setupInteractive(t, "\nn\n") // Enter = default (opus), decline saving
	_, errOut, err := run(t, "ask", "why slow")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "Not saved; no file was changed. Next time use -m opus") {
		t.Errorf("output:\n%s", errOut)
	}
	if _, err := os.Stat(filepath.Join(home, "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("config written despite declining: %v", err)
	}
}

func TestAskModelFlagSkipsPrompt(t *testing.T) {
	setupInteractive(t, "")
	_, errOut, err := run(t, "ask", "-m", "haiku", "why slow")
	if err != nil || strings.Contains(errOut, "Choose a model") {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "anthropic/"+modelID("haiku")) {
		t.Errorf("header should show resolved model:\n%s", errOut)
	}
}

func TestAskNonInteractiveUsesDefault(t *testing.T) {
	setupInteractive(t, "")
	interactive = func() bool { return false }
	_, errOut, err := run(t, "ask", "why slow")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "note: no model configured; using opus") {
		t.Errorf("output:\n%s", errOut)
	}
}

func TestPickModelRetriesInvalidInput(t *testing.T) {
	setupInteractive(t, "")
	cfg := &config.Config{}
	def, _ := cfg.DefaultChoice(os.Getenv)
	var out bytes.Buffer
	ch, err := pickModel(bufio.NewReader(strings.NewReader("99\nnope\nhaiku\n")), &out, cfg.Choices(os.Getenv), def, false)
	if err != nil || ch.Name != "haiku" {
		t.Fatalf("%+v %v\n%s", ch, err, out.String())
	}
	if strings.Count(out.String(), "Not an available model") != 2 {
		t.Errorf("output:\n%s", out.String())
	}
	ch, err = pickModel(bufio.NewReader(strings.NewReader("anthropic/claude-custom\n")), &out, cfg.Choices(os.Getenv), def, false)
	if err != nil || ch.Provider != "anthropic" || ch.Model != "claude-custom" {
		t.Fatalf("provider/model: %+v %v", ch, err)
	}
}

func TestModelsCommand(t *testing.T) {
	setupInteractive(t, "")
	out, _, err := run(t, "models")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"opus", "✓ ANTHROPIC_API_KEY", "✗ DEEPSEEK_API_KEY", "* anthropic/" + modelID("opus") + " (default"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestInitWizardWithKey(t *testing.T) {
	home := setupInteractive(t, "2\n\norders\n订单主库\n\n") // sonnet, add target, name, description, default env
	_, errOut, err := run(t, "init")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(home, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Model != "sonnet" || len(cfg.Targets) != 1 {
		t.Fatalf("cfg: model=%q targets=%+v", cfg.LLM.Model, cfg.Targets)
	}
	tg := cfg.Targets[0]
	if tg.Name != "orders" || tg.Description != "订单主库" || tg.DSNEnv != "YUANFANG_ORDERS_DSN" || tg.Type != "mysql" {
		t.Fatalf("target: %+v", tg)
	}
	if !strings.Contains(errOut, "export YUANFANG_ORDERS_DSN=") || strings.Contains(errOut, "export ANTHROPIC_API_KEY") {
		t.Errorf("next steps:\n%s", errOut)
	}
}

func TestInitWizardWithoutKey(t *testing.T) {
	home := setupInteractive(t, "\n\nn\n") // default model, skip the key, no target
	t.Setenv("ANTHROPIC_API_KEY", "")
	_, errOut, err := run(t, "init")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"No LLM API key is set yet", "needs ANTHROPIC_API_KEY", "needs DEEPSEEK_API_KEY", "export ANTHROPIC_API_KEY=", "yuanfang toolset check"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("missing %q:\n%s", want, errOut)
		}
	}
	cfg, err := config.Load(filepath.Join(home, "config.yaml"))
	if err != nil || cfg.LLM.Model != "opus" || len(cfg.Targets) != 0 {
		t.Fatalf("cfg: %+v %v", cfg, err)
	}
}

func TestBareYuanfangOffersSetup(t *testing.T) {
	home := setupInteractive(t, "n\n")
	out, errOut, err := run(t)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "No Yuanfang configuration found") || !strings.Contains(out, "Available Commands") {
		t.Fatalf("declined setup should show help:\n%s\n%s", errOut, out)
	}

	promptInput = strings.NewReader("y\n3\nn\n") // set up: haiku, no target
	if _, _, err := run(t); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(home, "config.yaml"))
	if err != nil || cfg.LLM.Model != "haiku" {
		t.Fatalf("cfg: %+v %v", cfg.LLM, err)
	}

	// With a config present, bare yuanfang only prints help.
	promptInput = strings.NewReader("")
	_, errOut, err = run(t)
	if err != nil || strings.Contains(errOut, "No Yuanfang configuration") {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if _, _, err := run(t, "aks"); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("typo should still be an unknown command: %v", err)
	}
}

func TestAskWithoutKeyExplainsWhatToSet(t *testing.T) {
	setupInteractive(t, "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	_, _, err := run(t, "ask", "why slow")
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"no LLM API key found", "export ANTHROPIC_API_KEY=", "opus, sonnet", "export DEEPSEEK_API_KEY=", "yuanfang init"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q:\n%s", want, err)
		}
	}
}

func TestInitWizardSavesKeyAndDSN(t *testing.T) {
	// default model, paste key, add target, name, no description,
	// host, bad port, port, default user, password with special characters
	key := fakeSecret("api-key")
	password := fakeSecret("db-password") + "@/:)" // characters a DSN must escape correctly
	home := setupInteractive(t, "\n"+key+"\n\norders\n\n127.0.0.1\n99999\n1\n\n"+password+"\n")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("YUANFANG_ORDERS_DSN", "")
	_, errOut, err := run(t, "init")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Paste the API key for opus, saved as ANTHROPIC_API_KEY",
		"Saved ANTHROPIC_API_KEY to " + filepath.Join(home, "credentials") + " (" + config.Mask(key) + ")",
		"Host or IP",
		"Port [3306]",
		"invalid: use a number from 1 to 65535",
		"User [yuanfang]",
		"Password (input hidden)",
		"Saved YUANFANG_ORDERS_DSN to " + filepath.Join(home, "credentials") + " (yuanfang@127.0.0.1:1)",
		"Files written:",
		filepath.Join(home, "config.yaml") + "  created: model opus, target orders",
		filepath.Join(home, "credentials") + "  created: ANTHROPIC_API_KEY, YUANFANG_ORDERS_DSN",
		"could not connect",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("missing %q:\n%s", want, errOut)
		}
	}
	for _, secret := range []string{key, password} {
		if strings.Contains(errOut, secret) {
			t.Errorf("secret %q echoed:\n%s", secret, errOut)
		}
	}
	if strings.Contains(errOut, "export ANTHROPIC_API_KEY") || strings.Contains(errOut, "export YUANFANG_ORDERS_DSN") {
		t.Errorf("entered values should not be listed as todo:\n%s", errOut)
	}

	creds, err := os.ReadFile(filepath.Join(home, "credentials"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(creds), "ANTHROPIC_API_KEY="+key) || !strings.Contains(string(creds), "YUANFANG_ORDERS_DSN=yuanfang:"+password+"@tcp(127.0.0.1:1)/") {
		t.Fatalf("credentials:\n%s", creds)
	}
	if st, _ := os.Stat(filepath.Join(home, "credentials")); st.Mode().Perm() != 0o600 {
		t.Errorf("credentials mode %v", st.Mode().Perm())
	}
	cfgText, _ := os.ReadFile(filepath.Join(home, "config.yaml"))
	if strings.Contains(string(cfgText), key) || strings.Contains(string(cfgText), password) {
		t.Fatalf("secret leaked into config.yaml:\n%s", cfgText)
	}

	// A later run picks the key up from the credentials file.
	t.Setenv("ANTHROPIC_API_KEY", "")
	promptInput = strings.NewReader("")
	out, _, err := run(t, "models")
	if err != nil || !strings.Contains(out, "✓ ANTHROPIC_API_KEY") {
		t.Fatalf("credentials not loaded: %v\n%s", err, out)
	}
}

func TestAskMySQLConnectionIPv6AndSkip(t *testing.T) {
	var out bytes.Buffer
	pw := fakeSecret("db-password")
	dsn, summary, err := askMySQLConnection(bufio.NewReader(strings.NewReader("::1\n\nops\n"+pw+"\n")), &out)
	if err != nil || dsn != "ops:"+pw+"@tcp([::1]:3306)/" || summary != "ops@[::1]:3306" {
		t.Fatalf("dsn=%q summary=%q err=%v", dsn, summary, err)
	}
	dsn, _, err = askMySQLConnection(bufio.NewReader(strings.NewReader("\n")), &out)
	if err != nil || dsn != "" {
		t.Fatalf("Enter on host should skip: %q %v", dsn, err)
	}
	_, _, err = askMySQLConnection(bufio.NewReader(strings.NewReader("bad host\nalso bad\nstill bad\n")), &out)
	if err == nil {
		t.Fatal("invalid hosts accepted")
	}
}

func TestInitWizardReportsUpdatedFiles(t *testing.T) {
	first, second := fakeSecret("api-key"), fakeSecret("api-key")
	home := setupInteractive(t, "\n"+first+"\nn\n")
	t.Setenv("ANTHROPIC_API_KEY", "")
	if _, _, err := run(t, "init"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "")
	promptInput = strings.NewReader("\ny\n" + second + "\nn\n") // model, replace the saved key, new key, no target
	_, errOut, err := run(t, "init", "--force")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ANTHROPIC_API_KEY is already saved in " + filepath.Join(home, "credentials") + " (" + config.Mask(first) + "). Replace it? [y/N]",
		filepath.Join(home, "config.yaml") + "  overwritten: model opus",
		filepath.Join(home, "credentials") + "  updated: ANTHROPIC_API_KEY",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("missing %q:\n%s", want, errOut)
		}
	}
	creds, _ := os.ReadFile(filepath.Join(home, "credentials"))
	if strings.Count(string(creds), "ANTHROPIC_API_KEY=") != 1 || !strings.Contains(string(creds), second) {
		t.Fatalf("credentials:\n%s", creds)
	}
}

func TestModelsFetch(t *testing.T) {
	home := setupInteractive(t, "")
	t.Setenv("DEEPSEEK_API_KEY", fakeSecret("api-key"))
	t.Setenv("OLLAMA_KEY", fakeSecret("api-key"))
	cfgPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("models:\n  local: { provider: openai, model: qwen3:32b, base_url: \"http://ollama:11434/v1\", api_key_env: OLLAMA_KEY }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	day := func(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }
	opus := modelID("opus")
	newerOpus, olderOpus := family(opus)+"-99", family(opus)+"-1" // same family, either side of opus
	listModels = func(_ context.Context, r *config.ResolvedLLM) ([]llm.ModelInfo, error) {
		switch {
		case r.Preset == "anthropic":
			return []llm.ModelInfo{
				{ID: newerOpus, Created: day("2026-12-01")},
				{ID: modelID("sonnet"), Created: day("2026-09-10")},
				{ID: opus, Created: day("2026-09-10")},
				{ID: modelID("haiku"), Created: day("2026-08-01")},
				{ID: olderOpus, Created: day("2026-04-01")},
			}, nil
		case r.BaseURL == "http://ollama:11434/v1":
			return []llm.ModelInfo{{ID: "qwen3:32b"}, {ID: "llama4"}}, nil
		default:
			return nil, errors.New("connection refused")
		}
	}
	t.Cleanup(func() {
		listModels = func(ctx context.Context, r *config.ResolvedLLM) ([]llm.ModelInfo, error) { return nil, nil }
	})

	out, _, err := run(t, "models", "fetch")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"anthropic (ANTHROPIC_API_KEY)",
		newerOpus + " 2026-12-01 newer than \"opus\" (" + opus + ")",
		opus + " 2026-09-10 ← opus",
		"! \"fable\" → " + modelID("fable") + " is not in this list",
		"yuanfang ask -m anthropic/" + newerOpus,
		"models: { opus: { provider: anthropic, model: " + newerOpus + " } }",
		"deepseek (DEEPSEEK_API_KEY, https://api.deepseek.com)\n ✗ connection refused",
		`openai, alias "local" (OLLAMA_KEY, http://ollama:11434/v1)`,
		"qwen3:32b - ← local",
	} {
		if !strings.Contains(squeeze(out), want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(squeeze(out), olderOpus+" 2026-04-01 newer") {
		t.Errorf("older model flagged as newer:\n%s", out)
	}
}

// modelID returns the model ID a built-in short name currently points to, so
// tests keep passing when a built-in model is updated.
func modelID(name string) string {
	for _, b := range config.BuiltinModels {
		if b.Name == name {
			return b.Model
		}
	}
	panic("no built-in model " + name)
}

// squeeze collapses runs of spaces, so assertions do not depend on column widths.
func squeeze(s string) string { return regexp.MustCompile(` {2,}`).ReplaceAllString(s, " ") }

func TestFamily(t *testing.T) {
	for id, want := range map[string]string{"claude-opus-5-5": "claude-opus", "claude-opus-6": "claude-opus", "deepseek-chat": "deepseek-chat", "qwen-max": "qwen-max"} {
		if got := family(id); got != want {
			t.Errorf("family(%q) = %q, want %q", id, got, want)
		}
	}
}

// fakeSecret returns a random value that is obviously not a real credential,
// so tests carry no secret-looking literals.
func fakeSecret(label string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "fake-" + label + "-" + hex.EncodeToString(b)
}

type brokenProvider struct{}

func (brokenProvider) Name() string  { return "fake" }
func (brokenProvider) Model() string { return "fake-1" }
func (brokenProvider) Chat(context.Context, *llm.Request) (*llm.Response, error) {
	return nil, errors.New("upstream timeout")
}

func latestCaseID(t *testing.T) string {
	t.Helper()
	out, _, err := run(t, "case", "list")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(strings.Split(out, "\n")[1])[0]
}

func TestCaseShowTranscript(t *testing.T) {
	setupInteractive(t, "")
	if _, _, err := run(t, "ask", "-m", "opus", "why slow"); err != nil {
		t.Fatal(err)
	}
	id := latestCaseID(t)
	out, _, err := run(t, "case", "show", id, "--transcript")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"case " + id, "complaint: why slow", "settings: ", "thinking_summary=true",
		"model: fake/fake-1", "system prompt: ", "── step 1 ──", "Complaint: why slow",
		"model reply (", "submit_diagnosis", "results (final step; not sent to the model)",
		"submit_diagnosis: accepted", "end: closed after 1 step,",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestFailedCaseKeepsTranscript(t *testing.T) {
	setupInteractive(t, "")
	newProvider = func(*config.ResolvedLLM) (llm.Provider, error) { return brokenProvider{}, nil }
	_, _, err := run(t, "ask", "-m", "opus", "why slow")
	if err == nil || !strings.Contains(err.Error(), "--transcript") {
		t.Fatalf("error should point to the transcript: %v", err)
	}
	id := latestCaseID(t)
	out, _, err := run(t, "case", "show", id, "--transcript")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"! llm: upstream timeout", "end: failed after 0 steps", "error: upstream timeout"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	_, _, err = run(t, "case", "show", id)
	if err == nil || !strings.Contains(err.Error(), "yuanfang case show "+id+" --transcript") {
		t.Fatalf("report-less case should suggest the transcript: %v", err)
	}
}
