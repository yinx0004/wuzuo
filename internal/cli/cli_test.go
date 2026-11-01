// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yinx0004/wuzuo/internal/config"
	"github.com/yinx0004/wuzuo/internal/llm"
)

type oneShot struct{ calls int }

func (o *oneShot) Name() string  { return "fake" }
func (o *oneShot) Model() string { return "fake-1" }
func (o *oneShot) Chat(context.Context, *llm.Request) (*llm.Response, error) {
	o.calls++
	args := `{"title":"无法判断","summary":"没有可勘验的对象。","cause_of_death":{"statement":"未确认","confidence":"low","evidence":[]},"open_questions":["配置一个目标"]}`
	return &llm.Response{
		Message:    llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "submit_autopsy", Args: json.RawMessage(args)}}},
		StopReason: llm.StopToolUse,
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
	t.Setenv("WUZUO_HOME", home)
	t.Setenv("WUZUO_CONFIG", filepath.Join(home, "none.yaml"))
	t.Setenv("WUZUO_MYSQL_DSN", "")
	t.Setenv("WUZUO_PROVIDER", "anthropic")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	fake := &oneShot{}
	newProvider = func(*config.ResolvedLLM) (llm.Provider, error) { return fake, nil }
	t.Cleanup(func() { newProvider = llm.New })

	out, _, err := run(t, "ask", "为什么订单库变慢了")
	if err != nil {
		t.Fatal(err)
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

// setupInteractive isolates WuZuo's home and fakes a terminal whose user
// types answers.
func setupInteractive(t *testing.T, answers string) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("WUZUO_HOME", home)
	t.Setenv("WUZUO_CONFIG", "")
	t.Setenv("WUZUO_MODEL", "")
	t.Setenv("WUZUO_PROVIDER", "")
	t.Setenv("WUZUO_MYSQL_DSN", "")
	for _, k := range []string{"ANTHROPIC_AUTH_TOKEN", "OPENAI_API_KEY", "DEEPSEEK_API_KEY", "DASHSCOPE_API_KEY", "ARK_API_KEY"} {
		t.Setenv(k, "")
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
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
	if !strings.Contains(errOut, "anthropic/claude-haiku-5-5") {
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
	for _, want := range []string{"opus", "✓ ANTHROPIC_API_KEY", "✗ DEEPSEEK_API_KEY", "* anthropic/claude-opus-5-5 (default"} {
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
	if tg.Name != "orders" || tg.Description != "订单主库" || tg.DSNEnv != "WUZUO_ORDERS_DSN" || tg.Type != "mysql" {
		t.Fatalf("target: %+v", tg)
	}
	if !strings.Contains(errOut, "export WUZUO_ORDERS_DSN=") || strings.Contains(errOut, "export ANTHROPIC_API_KEY") {
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
	for _, want := range []string{"No LLM API key is set yet", "needs ANTHROPIC_API_KEY", "needs DEEPSEEK_API_KEY", "export ANTHROPIC_API_KEY=", "wuzuo toolset check"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("missing %q:\n%s", want, errOut)
		}
	}
	cfg, err := config.Load(filepath.Join(home, "config.yaml"))
	if err != nil || cfg.LLM.Model != "opus" || len(cfg.Targets) != 0 {
		t.Fatalf("cfg: %+v %v", cfg, err)
	}
}

func TestBareWuzuoOffersSetup(t *testing.T) {
	home := setupInteractive(t, "n\n")
	out, errOut, err := run(t)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "No WuZuo configuration found") || !strings.Contains(out, "Available Commands") {
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

	// With a config present, bare wuzuo only prints help.
	promptInput = strings.NewReader("")
	_, errOut, err = run(t)
	if err != nil || strings.Contains(errOut, "No WuZuo configuration") {
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
	for _, want := range []string{"no LLM API key found", "export ANTHROPIC_API_KEY=", "opus, sonnet", "export DEEPSEEK_API_KEY=", "wuzuo init"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q:\n%s", want, err)
		}
	}
}

func TestInitWizardSavesKeyAndDSN(t *testing.T) {
	// default model, paste key, add target, name, no description,
	// host, bad port, port, default user, password with special characters
	home := setupInteractive(t, "\nsk-ant-test-0123456789\n\norders\n\n127.0.0.1\n99999\n1\n\np@ss/w:rd)\n")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("WUZUO_ORDERS_DSN", "")
	_, errOut, err := run(t, "init")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Paste the API key for opus, saved as ANTHROPIC_API_KEY",
		"Saved ANTHROPIC_API_KEY to " + filepath.Join(home, "credentials") + " (sk-ant…6789)",
		"Host or IP",
		"Port [3306]",
		"invalid: use a number from 1 to 65535",
		"User [wuzuo]",
		"Password (input hidden)",
		"Saved WUZUO_ORDERS_DSN to " + filepath.Join(home, "credentials") + " (wuzuo@127.0.0.1:1)",
		"Files written:",
		filepath.Join(home, "config.yaml") + "  created: model opus, target orders",
		filepath.Join(home, "credentials") + "  created: ANTHROPIC_API_KEY, WUZUO_ORDERS_DSN",
		"could not connect",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("missing %q:\n%s", want, errOut)
		}
	}
	for _, secret := range []string{"sk-ant-test-0123456789", "p@ss/w:rd)"} {
		if strings.Contains(errOut, secret) {
			t.Errorf("secret %q echoed:\n%s", secret, errOut)
		}
	}
	if strings.Contains(errOut, "export ANTHROPIC_API_KEY") || strings.Contains(errOut, "export WUZUO_ORDERS_DSN") {
		t.Errorf("entered values should not be listed as todo:\n%s", errOut)
	}

	creds, err := os.ReadFile(filepath.Join(home, "credentials"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(creds), "ANTHROPIC_API_KEY=sk-ant-test-0123456789") || !strings.Contains(string(creds), "WUZUO_ORDERS_DSN=wuzuo:p@ss/w:rd)@tcp(127.0.0.1:1)/") {
		t.Fatalf("credentials:\n%s", creds)
	}
	if st, _ := os.Stat(filepath.Join(home, "credentials")); st.Mode().Perm() != 0o600 {
		t.Errorf("credentials mode %v", st.Mode().Perm())
	}
	cfgText, _ := os.ReadFile(filepath.Join(home, "config.yaml"))
	if strings.Contains(string(cfgText), "sk-ant") || strings.Contains(string(cfgText), "p@ss") {
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
	dsn, summary, err := askMySQLConnection(bufio.NewReader(strings.NewReader("::1\n\nops\npw\n")), &out)
	if err != nil || dsn != "ops:pw@tcp([::1]:3306)/" || summary != "ops@[::1]:3306" {
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
	home := setupInteractive(t, "\nsk-ant-first-0123456789\nn\n")
	t.Setenv("ANTHROPIC_API_KEY", "")
	if _, _, err := run(t, "init"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "")
	promptInput = strings.NewReader("\ny\nsk-ant-second-0123456789\nn\n") // model, replace the saved key, new key, no target
	_, errOut, err := run(t, "init", "--force")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ANTHROPIC_API_KEY is already saved in " + filepath.Join(home, "credentials") + " (sk-ant…6789). Replace it? [y/N]",
		filepath.Join(home, "config.yaml") + "  overwritten: model opus",
		filepath.Join(home, "credentials") + "  updated: ANTHROPIC_API_KEY",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("missing %q:\n%s", want, errOut)
		}
	}
	creds, _ := os.ReadFile(filepath.Join(home, "credentials"))
	if strings.Count(string(creds), "ANTHROPIC_API_KEY=") != 1 || !strings.Contains(string(creds), "sk-ant-second") {
		t.Fatalf("credentials:\n%s", creds)
	}
}
