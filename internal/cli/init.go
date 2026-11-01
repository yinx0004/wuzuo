// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/yinx0004/wuzuo/internal/config"
)

func newInitCmd(g *globalFlags) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a starter config file",
		Long: `Write a commented starter config to ~/.wuzuo/config.yaml (or --config).
In a terminal it lets you choose the default model from those whose API key
is set; otherwise it uses the default WuZuo would pick.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := g.configPathOrDefault()
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", path)
			} else if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			cfg := &config.Config{}
			model := ""
			if def, ok := cfg.DefaultChoice(envLookup); ok {
				model = def.Name
				if interactive() {
					chosen, err := pickModel(bufio.NewReader(promptInput), cmd.ErrOrStderr(), cfg.Choices(envLookup), def)
					if err != nil {
						return err
					}
					model = chosen.Name
				}
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "no LLM API key found yet; set one (see docs/configuration.md) and fill in llm.model\n")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(config.Template(model)), 0o600); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\nnext: add a target, then run `wuzuo toolset check`\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing file")
	return cmd
}
