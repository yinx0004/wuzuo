// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/yinx0004/wuzuo/internal/agent"
	"github.com/yinx0004/wuzuo/internal/cases"
	"github.com/yinx0004/wuzuo/internal/evidence"
	"github.com/yinx0004/wuzuo/pkg/toolset"
)

func newCaseCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "case", Short: "Inspect and re-examine past investigations"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List cases, newest first",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := g.load()
			if err != nil {
				return err
			}
			all, err := cases.Store{Root: cfg.CasesDir}.List()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tSTATUS\tMODEL\tSTEPS\tCOMPLAINT")
			for _, c := range all {
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", c.ID, c.Status, c.Model, c.Steps, toolset.Clip(c.Question, 60))
			}
			return w.Flush()
		},
	}

	var asJSON bool
	show := &cobra.Command{
		Use:   "show <case-id>",
		Short: "Print a case's autopsy report (ID prefixes work)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := g.load()
			if err != nil {
				return err
			}
			store := cases.Store{Root: cfg.CasesDir}
			c, err := store.Load(args[0])
			if err != nil {
				return err
			}
			a, err := store.LoadReport(c.ID)
			if err != nil {
				return fmt.Errorf("case %s has no report (status %s): %s", c.ID, c.Status, c.Error)
			}
			if asJSON {
				return printReport(cmd.OutOrStdout(), true, a, "")
			}
			md, err := os.ReadFile(filepath.Join(store.Dir(c.ID), "report.md"))
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(md)
			return err
		},
	}
	show.Flags().BoolVar(&asJSON, "json", false, "print JSON")

	var out runOutput
	replay := &cobra.Command{
		Use:   "replay <case-id>",
		Short: "Re-examine a case's sealed evidence (e.g. with another model) without touching any system",
		Example: `  wuzuo case replay 20261101-1530 -m sonnet
  wuzuo case replay 20261101-1530 -m deepseek`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := g.load()
			if err != nil {
				return err
			}
			store := cases.Store{Root: cfg.CasesDir}
			orig, err := store.Load(args[0])
			if err != nil {
				return err
			}
			locker, err := evidence.Load(store.EvidenceDir(orig.ID))
			if err != nil {
				return fmt.Errorf("open evidence of %s: %w", orig.ID, err)
			}
			if len(locker.All()) == 0 {
				return fmt.Errorf("case %s has no evidence to re-examine", orig.ID)
			}
			// The replay is a new case that refers to the original's evidence.
			return runCase(cmd, g, cfg, out, caseRun{
				question: orig.Question,
				targets:  orig.Targets,
				input: agent.Input{
					Question:      orig.Question,
					Replay:        locker.All(),
					ReplayExcerpt: cfg.Safety.MaxEvidenceChars,
				},
				replayOf: orig.ID,
				locker:   locker,
			})
		},
	}
	replay.Flags().BoolVar(&out.json, "json", false, "print the report as JSON")
	replay.Flags().BoolVarP(&out.quiet, "quiet", "q", false, "do not print progress")
	addModelFlag(replay, g, &out)

	cmd.AddCommand(list, show, replay)
	return cmd
}
