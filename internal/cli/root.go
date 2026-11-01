// SPDX-License-Identifier: Apache-2.0

// Package cli implements the wuzuo command line.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/yinx0004/wuzuo/internal/config"
	"github.com/yinx0004/wuzuo/internal/version"
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
		Use:   "wuzuo",
		Short: "WuZuo (仵作) — AI forensic examiner for production incidents",
		Long: `WuZuo investigates production incidents the way a forensic examiner works a
scene: it forms hypotheses, collects read-only evidence, rules causes out,
and writes an autopsy report where every conclusion cites sealed evidence.`,
		Version:       fmt.Sprintf("%s (commit %s, built %s)", version.Version, version.Commit, version.Date),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVarP(&g.configPath, "config", "c", "", "config file (default $WUZUO_CONFIG or ~/.wuzuo/config.yaml)")
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
