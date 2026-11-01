// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/yinx0004/wuzuo/internal/agent"
	"github.com/yinx0004/wuzuo/internal/cases"
	"github.com/yinx0004/wuzuo/internal/config"
	"github.com/yinx0004/wuzuo/internal/evidence"
	"github.com/yinx0004/wuzuo/internal/llm"
	"github.com/yinx0004/wuzuo/internal/report"
	"github.com/yinx0004/wuzuo/pkg/toolset"
)

func envLookup(k string) string { return os.Getenv(k) }

// newProvider is replaced in tests.
var newProvider = llm.New

type runOutput struct {
	json  bool
	quiet bool
	// model is the -m flag: a short name, provider/model, or model ID.
	model string
}

func addModelFlag(cmd *cobra.Command, g *globalFlags, out *runOutput) {
	cmd.Flags().StringVarP(&out.model, "model", "m", "", "model to use: a name from `wuzuo models` (opus, sonnet, deepseek, ...) or provider/model-id")
	_ = cmd.RegisterFlagCompletionFunc("model", completeModels(g))
}

func newAskCmd(g *globalFlags) *cobra.Command {
	var targets []string
	var out runOutput
	cmd := &cobra.Command{
		Use:   "ask <complaint>",
		Short: "Investigate an incident and write an autopsy report",
		Example: `  wuzuo ask "orders database became slow after 10:00" -t prod-orders
  wuzuo ask "为什么订单库写入卡住了" --json > report.json`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := g.load()
			if err != nil {
				return err
			}
			question := strings.Join(args, " ")
			sel, err := cfg.SelectTargets(targets)
			if err != nil {
				return err
			}
			if len(sel) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: no targets configured; WuZuo can only reason, not examine. Set WUZUO_MYSQL_DSN or add targets to the config.")
			}
			sets, err := buildToolsets(cfg, sel)
			if err != nil {
				return err
			}
			defer closeAll(sets)
			var tools []toolset.Tool
			for _, s := range sets {
				tools = append(tools, s.Tools()...)
			}
			names := make([]string, len(sel))
			for i, t := range sel {
				names[i] = t.Name
			}
			return runCase(cmd, g, cfg, out, caseRun{
				question: question,
				targets:  names,
				input:    agent.Input{Question: question, Targets: agentTargets(sel)},
				tools:    tools,
			})
		},
	}
	cmd.Flags().StringSliceVarP(&targets, "target", "t", nil, "targets to examine (default: all configured)")
	cmd.Flags().BoolVar(&out.json, "json", false, "print the report as JSON instead of Markdown")
	cmd.Flags().BoolVarP(&out.quiet, "quiet", "q", false, "do not print progress to stderr")
	addModelFlag(cmd, g, &out)
	return cmd
}

type caseRun struct {
	question string
	targets  []string
	input    agent.Input
	tools    []toolset.Tool
	replayOf string
	// locker, if set, is reused instead of creating a new one (replay).
	locker *evidence.Locker
}

// runCase drives one investigation and persists everything about it.
func runCase(cmd *cobra.Command, g *globalFlags, cfg *config.Config, out runOutput, r caseRun) error {
	modelName, err := ensureModel(cmd, cfg, g.configPathOrDefault(), out.model)
	if err != nil {
		return err
	}
	resolved, err := cfg.ResolveModel(modelName, envLookup)
	if err != nil {
		return err
	}
	provider, err := newProvider(resolved)
	if err != nil {
		return err
	}
	store := cases.Store{Root: cfg.CasesDir}
	c := &cases.Case{
		ID:       cases.NewID(time.Now()),
		Question: r.question,
		Targets:  r.targets,
		Provider: provider.Name(),
		Model:    provider.Model(),
		Started:  time.Now().UTC(),
		Status:   cases.StatusOpen,
		ReplayOf: r.replayOf,
	}
	if err := store.Save(c); err != nil {
		return fmt.Errorf("create case: %w", err)
	}
	locker := r.locker
	if locker == nil {
		locker, err = evidence.NewLocker(store.EvidenceDir(c.ID), *cfg.Safety.Redact)
		if err != nil {
			return err
		}
	}

	progress := cmd.ErrOrStderr()
	if out.quiet {
		progress = io.Discard
	}
	fmt.Fprintf(progress, "case %s, model %s/%s (%s)\n", c.ID, resolved.Preset, resolved.Model, resolved.Source)

	res, runErr := agent.Investigate(cmd.Context(), agent.Options{
		Provider:         provider,
		Tools:            r.tools,
		Locker:           locker,
		MaxSteps:         cfg.LLM.MaxSteps,
		MaxEvidenceChars: cfg.Safety.MaxEvidenceChars,
		OnEvent:          func(e agent.Event) { printEvent(progress, e) },
	}, r.input)

	c.Finished = time.Now().UTC()
	if res != nil {
		c.Steps = res.Steps
		c.Hypotheses = res.Hypotheses
		c.Tokens = cases.Tokens{
			Input:      res.Usage.InputTokens,
			Output:     res.Usage.OutputTokens,
			CacheRead:  res.Usage.CacheReadTokens,
			CacheWrite: res.Usage.CacheWriteTokens,
		}
	}
	if runErr != nil {
		c.Status, c.Error = cases.StatusFailed, runErr.Error()
		_ = store.Save(c)
		return fmt.Errorf("case %s: %w (case files kept in %s)", c.ID, runErr, displayPath(store.Dir(c.ID)))
	}
	c.Status = cases.StatusClosed
	if err := store.Save(c); err != nil {
		return err
	}

	md := report.Markdown(res.Autopsy, metaOf(c), locker.All(), report.DetectLang(r.question))
	if err := store.SaveReport(c.ID, res.Autopsy, md); err != nil {
		return err
	}
	fmt.Fprintf(progress, "closed in %s: %d steps, %d evidence, %s\n",
		c.Finished.Sub(c.Started).Round(time.Second), c.Steps, len(locker.All()), tokenSummary(c.Tokens))
	fmt.Fprintf(progress, "case files: %s/ (case.json, report.md, report.json, evidence/)\n\n", displayPath(store.Dir(c.ID)))
	return printReport(cmd.OutOrStdout(), out.json, res.Autopsy, md)
}

func metaOf(c *cases.Case) report.Meta {
	return report.Meta{
		CaseID: c.ID, Question: c.Question, Targets: c.Targets, Provider: c.Provider, Model: c.Model,
		Started: c.Started, Finished: c.Finished, Steps: c.Steps, Tokens: c.Tokens.TotalInput() + c.Tokens.Output,
	}
}

func printReport(w io.Writer, asJSON bool, a *report.Autopsy, md string) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(a)
	}
	_, err := io.WriteString(w, md)
	return err
}

func printEvent(w io.Writer, e agent.Event) {
	switch e.Kind {
	case agent.EventHypothesis:
		fmt.Fprintf(w, "  hypothesis  %s\n", e.Text)
	case agent.EventProbe:
		fmt.Fprintf(w, "  probe       %s\n", toolset.Clip(e.Text, 160))
	case agent.EventEvidence:
		status := "saved"
		if e.Evidence.Error != "" {
			status = "failed: " + toolset.Clip(e.Evidence.Error, 120)
		}
		fmt.Fprintf(w, "  evidence    %s %s %s\n", e.Evidence.ID, e.Evidence.Tool, status)
	case agent.EventRuledOut:
		fmt.Fprintf(w, "  ruled out   %s\n", toolset.Clip(e.Text, 200))
	case agent.EventRejected:
		fmt.Fprintf(w, "  rejected    report sent back: %s\n", toolset.Clip(e.Text, 300))
	case agent.EventNarration:
		fmt.Fprintf(w, "  model       %s\n", toolset.Clip(strings.Join(strings.Fields(e.Text), " "), 240))
	}
}

// tokenSummary reports all input, including the share served from the prompt
// cache, and output.
func tokenSummary(t cases.Tokens) string {
	in := fmt.Sprintf("%d in", t.TotalInput())
	if cached := t.CacheRead + t.CacheWrite; cached > 0 {
		in = fmt.Sprintf("%d in (%d read from cache, %d written to cache)", t.TotalInput(), t.CacheRead, t.CacheWrite)
	}
	return fmt.Sprintf("tokens: %s / %d out", in, t.Output)
}
