// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yinx0004/yuanfang/internal/agent"
	"github.com/yinx0004/yuanfang/internal/config"
	"github.com/yinx0004/yuanfang/pkg/redact"
	"github.com/yinx0004/yuanfang/pkg/toolset"
	"github.com/yinx0004/yuanfang/toolsets/mysql"
)

// buildToolsets instantiates toolsets for the selected targets.
func buildToolsets(cfg *config.Config, targets []config.Target) ([]toolset.Toolset, error) {
	var my []mysql.Target
	for _, t := range targets {
		dsn, err := t.ResolveDSN()
		if err != nil {
			return nil, err
		}
		if t.Type == "mysql" {
			my = append(my, mysql.Target{Name: t.Name, DSN: dsn, Description: t.Description})
		}
	}
	var out []toolset.Toolset
	if len(my) > 0 {
		ts, err := mysql.New(mysql.Options{
			Targets:      my,
			QueryTimeout: cfg.Safety.QueryTimeout.Std(),
			Redact:       *cfg.Safety.Redact,
			AllowExplain: cfg.Safety.AllowExplain,
			AllowRawSQL:  cfg.Safety.AllowRawSQL,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, ts)
	}
	return out, nil
}

func closeAll(sets []toolset.Toolset) {
	for _, s := range sets {
		_ = s.Close()
	}
}

func agentTargets(ts []config.Target) []agent.Target {
	out := make([]agent.Target, len(ts))
	for i, t := range ts {
		out[i] = agent.Target{Name: t.Name, Type: t.Type, Description: t.Description}
	}
	return out
}

func newToolsetCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "toolset", Short: "List toolsets or check their prerequisites"}
	var targets []string
	list := &cobra.Command{
		Use:   "list",
		Short: "List available probes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := g.load()
			if err != nil {
				return err
			}
			sel, err := cfg.SelectTargets(targets)
			if err != nil {
				return err
			}
			sets, err := buildToolsets(cfg, sel)
			if err != nil {
				return err
			}
			defer closeAll(sets)
			out := cmd.OutOrStdout()
			if len(sets) == 0 {
				fmt.Fprintln(out, "No targets configured. Add targets to the config file or set YUANFANG_MYSQL_DSN.")
				return nil
			}
			for _, s := range sets {
				fmt.Fprintf(out, "%s — %s\n", s.Name(), s.Description())
				for _, t := range s.Tools() {
					spec := t.Spec()
					fmt.Fprintf(out, "  %-22s %s\n", spec.Name, firstSentence(spec.Description))
				}
			}
			return nil
		},
	}
	check := &cobra.Command{
		Use:   "check",
		Short: "Verify connectivity, privileges and server features for each target",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := g.load()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			failed := 0
			if !cfg.ModelChosen("", envLookup) {
				if def, ok := cfg.DefaultChoice(envLookup); ok {
					fmt.Fprintf(out, "✓ llm: no model configured; default would be %s (%s/%s), and `yuanfang ask` will ask you to choose\n",
						def.Name, def.Provider, def.Model)
				} else if _, err := cfg.ResolveModel("", envLookup); err != nil {
					fmt.Fprintf(out, "✗ llm: %v\n", err)
					failed++
				}
			} else if r, err := cfg.ResolveModel("", envLookup); err != nil {
				fmt.Fprintf(out, "✗ llm: %v\n", err)
				failed++
			} else {
				fmt.Fprintf(out, "✓ llm: %s/%s (%s)\n", r.Preset, r.Model, r.Source)
			}
			sel, err := cfg.SelectTargets(targets)
			if err != nil {
				return err
			}
			sets, err := buildToolsets(cfg, sel)
			if err != nil {
				return err
			}
			defer closeAll(sets)
			for _, s := range sets {
				for _, r := range s.Check(cmd.Context()) {
					mark := "✓"
					if !r.OK {
						mark = "✗"
						failed++
					}
					fmt.Fprintf(out, "%s %s/%s: %s", mark, s.Name(), r.Target, r.Item)
					if r.Detail != "" {
						fmt.Fprintf(out, " — %s", r.Detail)
					}
					fmt.Fprintln(out)
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d check(s) failed", failed)
			}
			return nil
		},
	}
	run := &cobra.Command{
		Use:   "run <probe> [json-args]",
		Short: "Run a single probe by hand and print what the investigator would see",
		Example: `  yuanfang toolset run mysql_lock_waits
  yuanfang toolset run mysql_top_statements '{"order_by":"no_index","limit":5}'`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := g.load()
			if err != nil {
				return err
			}
			sel, err := cfg.SelectTargets(targets)
			if err != nil {
				return err
			}
			sets, err := buildToolsets(cfg, sel)
			if err != nil {
				return err
			}
			defer closeAll(sets)
			raw := "{}"
			if len(args) == 2 {
				raw = args[1]
			}
			if !json.Valid([]byte(raw)) {
				return fmt.Errorf("arguments must be a JSON object, got %q", raw)
			}
			for _, s := range sets {
				for _, t := range s.Tools() {
					if t.Spec().Name != args[0] {
						continue
					}
					res, err := t.Run(cmd.Context(), json.RawMessage(raw))
					if res != nil && res.Content != "" {
						content := res.Content
						if *cfg.Safety.Redact {
							content = redact.Secrets(content)
						}
						fmt.Fprint(cmd.OutOrStdout(), content)
					}
					return err
				}
			}
			if sw, ok := disabledProbes[args[0]]; ok {
				return fmt.Errorf("%s is disabled; set safety.%s: true in the config to enable it", args[0], sw)
			}
			return fmt.Errorf("no probe named %q (see `yuanfang toolset list`)", args[0])
		},
	}
	for _, c := range []*cobra.Command{list, check, run} {
		c.Flags().StringSliceVarP(&targets, "target", "t", nil, "targets (default: all configured)")
	}
	cmd.AddCommand(list, check, run)
	return cmd
}

// disabledProbes maps opt-in probes to the safety switch that enables them.
var disabledProbes = map[string]string{
	"mysql_explain": "allow_explain",
	"mysql_query":   "allow_raw_sql",
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}
