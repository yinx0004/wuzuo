// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/yinx0004/yuanfang/internal/config"
)

// interactive reports whether Yuanfang may prompt: stdin and stderr are both
// terminals. Replaced in tests.
var interactive = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}

// promptInput is where prompts read answers from. Replaced in tests.
var promptInput io.Reader = os.Stdin

// ensureModel returns the model name to use for this run. When the user has
// not chosen a model anywhere (-m, YUANFANG_MODEL, YUANFANG_PROVIDER, llm.model,
// llm.provider) and Yuanfang runs in a terminal, it lists the models whose keys
// are set, lets the user pick one (Enter takes the default), and offers to
// save the choice. Without a terminal it uses the default and says so.
func ensureModel(cmd *cobra.Command, cfg *config.Config, configPath, override string) (string, error) {
	if cfg.ModelChosen(override, envLookup) {
		return override, nil
	}
	def, ok := cfg.DefaultChoice(envLookup)
	if !ok {
		return override, nil // ResolveModel explains which keys to set
	}
	errOut := cmd.ErrOrStderr()
	if !interactive() {
		fmt.Fprintf(errOut, "note: no model configured; using %s (%s/%s). Choose with -m <name>; see `yuanfang models`.\n",
			def.Name, def.Provider, def.Model)
		return def.Name, nil
	}

	in := bufio.NewReader(promptInput)
	fmt.Fprintln(errOut, "No model is configured yet.")
	chosen, err := pickModel(in, errOut, cfg.Choices(envLookup), def, false)
	if err != nil {
		return "", err
	}
	save, err := confirm(in, errOut, fmt.Sprintf("Save %q as the default model in %s?", chosen.Name, displayPath(configPath)), true)
	if err != nil {
		return "", err
	}
	if save {
		existed := fileExists(configPath)
		if err := config.SaveDefaultModel(configPath, chosen.Name, chosen.Provider); err != nil {
			return "", fmt.Errorf("save default model: %w", err)
		}
		if existed {
			fmt.Fprintf(errOut, "Updated %s: llm.model = %s. Change it there, or per run with -m.\n\n", displayPath(configPath), chosen.Name)
		} else {
			fmt.Fprintf(errOut, "Created %s with llm.model = %s. Change it there, or per run with -m.\n\n", displayPath(configPath), chosen.Name)
		}
	} else {
		fmt.Fprintf(errOut, "Not saved; no file was changed. Next time use -m %s, or set llm.model in %s.\n\n", chosen.Name, displayPath(configPath))
	}
	return chosen.Name, nil
}

// pickModel shows the models and reads a choice: a number, a name,
// "provider/model", or Enter for the default. Normally only models whose key
// is set are offered; with allowMissingKey (first-time setup before any key
// exists) every named model is offered together with the key it needs.
func pickModel(in *bufio.Reader, out io.Writer, choices []config.Choice, def config.Choice, allowMissingKey bool) (config.Choice, error) {
	var avail []config.Choice
	missing := map[string][]string{} // key variable -> names
	for _, ch := range choices {
		switch {
		case ch.Available || (allowMissingKey && ch.Source != "env"):
			avail = append(avail, ch)
		case ch.Source != "env":
			missing[ch.KeyEnv] = append(missing[ch.KeyEnv], ch.Name)
		}
	}
	defIdx := 0
	fmt.Fprintln(out, "Choose a model for Yuanfang:")
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for i, ch := range avail {
		mark := ""
		if ch.Name == def.Name {
			mark, defIdx = "(default)", i
		}
		desc := ch.Description
		if allowMissingKey {
			desc = "needs " + ch.KeyEnv
			if ch.Available {
				desc = ch.KeyEnv + " is set"
			}
		}
		fmt.Fprintf(w, "  %d)\t%s\t%s/%s\t%s\t%s\n", i+1, ch.Name, ch.Provider, ch.Model, desc, mark)
	}
	_ = w.Flush()
	if len(missing) > 0 {
		keys := make([]string, 0, len(missing))
		for k := range missing {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintln(out, "Also supported once their key is set:")
		for _, k := range keys {
			fmt.Fprintf(out, "     %s — set %s\n", strings.Join(missing[k], ", "), k)
		}
	}

	for attempt := 0; attempt < 3; attempt++ {
		fmt.Fprintf(out, "Model [%d]: ", defIdx+1)
		line, err := readLine(in)
		if err != nil {
			return config.Choice{}, err
		}
		if line == "" {
			return avail[defIdx], nil
		}
		if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(avail) {
			return avail[n-1], nil
		}
		for _, ch := range avail {
			if strings.EqualFold(ch.Name, line) {
				return ch, nil
			}
		}
		if p, m, ok := strings.Cut(line, "/"); ok && p != "" && m != "" {
			p = strings.ToLower(p)
			if keyEnv, set, known := config.KeyEnvFor(p, envLookup); known && (set || allowMissingKey) {
				return config.Choice{Name: p + "/" + m, Provider: p, Model: m, KeyEnv: keyEnv, Available: set}, nil
			}
		}
		fmt.Fprintf(out, "Not an available model: %q. Enter a number from 1 to %d, a name, or provider/model.\n", line, len(avail))
	}
	return config.Choice{}, errors.New("no model chosen")
}

// confirm asks a yes/no question.
func confirm(in *bufio.Reader, out io.Writer, question string, def bool) (bool, error) {
	hint := "[Y/n]"
	if !def {
		hint = "[y/N]"
	}
	fmt.Fprintf(out, "%s %s ", question, hint)
	line, err := readLine(in)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(line) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func readLine(in *bufio.Reader) (string, error) {
	line, err := in.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		if errors.Is(err, io.EOF) {
			return "", errors.New("no answer (input closed)")
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// completeModels offers model names for -m completion.
func completeModels(g *globalFlags) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		cfg, err := g.load()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for _, ch := range cfg.Choices(envLookup) {
			names = append(names, ch.Name+"\t"+ch.Provider+"/"+ch.Model)
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

func newModelsCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List model names usable with -m and in llm.model",
		Long: `List the short model names Yuanfang understands: built-in names, your own
aliases from "models:" in the config, and models named by provider variables
such as OPENAI_MODEL. "*" marks the model a run would use now.

Any "provider/model-id" also works, e.g. -m openai/gpt-5 or -m ark/ep-xxxx.
Use "yuanfang models fetch" to see which models each provider offers right now.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := g.load()
			if err != nil {
				return err
			}
			current, currentNote := "", ""
			if cfg.ModelChosen("", envLookup) {
				if r, err := cfg.ResolveModel("", envLookup); err == nil {
					current = r.Preset + "/" + r.Model
					currentNote = "configured"
				}
			} else if def, ok := cfg.DefaultChoice(envLookup); ok {
				current = def.Provider + "/" + def.Model
				currentNote = "default; `yuanfang ask` will ask you to choose"
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "\tNAME\tPROVIDER\tMODEL\tKEY\tSOURCE")
			for _, ch := range cfg.Choices(envLookup) {
				mark := ""
				if current != "" && ch.Provider+"/"+ch.Model == current {
					mark = "*"
				}
				key := "✓ " + ch.KeyEnv
				if !ch.Available {
					key = "✗ " + ch.KeyEnv
					if ch.Source == "env" && ch.Model == "" {
						key = "✗ " + ch.Description
					}
				}
				model := ch.Model
				if model == "" {
					model = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", mark, ch.Name, ch.Provider, model, key, ch.Source)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			if current != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "\n* %s (%s)\n", current, currentNote)
			}
			return nil
		},
	}
	cmd.AddCommand(newModelsFetchCmd(g))
	return cmd
}
