// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/yinx0004/yuanfang/internal/config"
)

func newInitCmd(g *globalFlags) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up Yuanfang: choose a model, add a target, write the config file",
		Long: `In a terminal, walk through choosing the default model and adding a MySQL
target, then write ~/.yuanfang/config.yaml (or --config) and list what is left
to do. Without a terminal, write a commented starter file using the default
model.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := g.configPathOrDefault()
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", path)
			} else if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if interactive() {
				return runSetup(bufio.NewReader(promptInput), cmd.ErrOrStderr(), path)
			}
			model := ""
			if def, ok := (&config.Config{}).DefaultChoice(envLookup); ok {
				model = def.Name
			} else {
				fmt.Fprintln(cmd.ErrOrStderr(), "no LLM API key found yet; set one (see docs/configuration.md) and fill in llm.model")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(config.Template(model, nil)), 0o600); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created %s (starter config, model %q). No secrets were written.\nnext: add a target, then run `yuanfang toolset check`\n", displayPath(path), model)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing file")
	return cmd
}
