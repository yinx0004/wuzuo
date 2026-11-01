// SPDX-License-Identifier: Apache-2.0

// Package cli implements the yuanfang command line.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/yinx0004/yuanfang/internal/config"
	"github.com/yinx0004/yuanfang/internal/version"
)

type globalFlags struct {
	configPath string
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := newRoot()
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return 1
	}
	return 0
}

func newRoot() *cobra.Command {
	g := &globalFlags{}
	root := &cobra.Command{
		Use:   "yuanfang",
		Short: "Yuanfang (元芳) — AI investigator for production incidents",
		Long: `Yuanfang investigates production incidents from evidence: it forms
hypotheses, collects read-only evidence, rules causes out, and writes a
diagnosis report where every conclusion cites sealed evidence.`,
		Version:       fmt.Sprintf("%s (commit %s, built %s)", version.Version, version.Commit, version.Date),
		SilenceUsage:  true,
		SilenceErrors: true,
		// Secrets saved by setup live in $YUANFANG_HOME/credentials; export them
		// before any command runs. Variables already set take precedence.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			loaded, warning, err := config.LoadCredentials(config.CredentialsPath())
			fromCredentials = map[string]bool{}
			for _, k := range loaded {
				fromCredentials[k] = true
			}
			if warning != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", warning)
			}
			return err
		},
		// Bare `yuanfang`: on first use in a terminal, offer setup; otherwise help.
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := g.configPathOrDefault()
			if _, err := os.Stat(path); err == nil || !interactive() {
				return cmd.Help()
			}
			errOut := cmd.ErrOrStderr()
			in := bufio.NewReader(promptInput)
			fmt.Fprintf(errOut, "No Yuanfang configuration found (%s).\n", path)
			yes, err := confirm(in, errOut, "Set it up now?", true)
			if err != nil {
				return err
			}
			if !yes {
				fmt.Fprintln(errOut, "Run `yuanfang init` when you are ready.")
				fmt.Fprintln(errOut)
				return cmd.Help()
			}
			return runSetup(in, errOut, path)
		},
	}
	root.PersistentFlags().StringVarP(&g.configPath, "config", "c", "", "config file (default $YUANFANG_CONFIG or ~/.yuanfang/config.yaml)")
	root.AddCommand(
		newAskCmd(g),
		newCaseCmd(g),
		newToolsetCmd(g),
		newModelsCmd(g),
		newInitCmd(g),
	)
	return root
}

func (g *globalFlags) load() (*config.Config, error) {
	return config.Load(g.configPath)
}

// configPathOrDefault is the file a saved setting is written to.
func (g *globalFlags) configPathOrDefault() string {
	if g.configPath != "" {
		return g.configPath
	}
	return config.DefaultPath()
}
