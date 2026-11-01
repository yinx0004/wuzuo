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
	for _, want := range []string{"No model is configured yet.", "opus", "(default)", "deepseek, deepseek-r — set DEEPSEEK_API_KEY", "Saved."} {
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
	if !strings.Contains(errOut, "Not saved. Next time use -m opus") {
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
	ch, err := pickModel(bufio.NewReader(strings.NewReader("99\nnope\nhaiku\n")), &out, cfg.Choices(os.Getenv), def)
	if err != nil || ch.Name != "haiku" {
		t.Fatalf("%+v %v\n%s", ch, err, out.String())
	}
	if strings.Count(out.String(), "Not an available model") != 2 {
		t.Errorf("output:\n%s", out.String())
	}
	ch, err = pickModel(bufio.NewReader(strings.NewReader("anthropic/claude-custom\n")), &out, cfg.Choices(os.Getenv), def)
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
