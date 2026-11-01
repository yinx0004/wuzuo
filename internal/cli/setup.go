// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yinx0004/wuzuo/internal/config"
)

var (
	targetNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	envNameRe    = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

// runSetup walks through first-time configuration in a terminal: the default
// model, optionally a MySQL target, then writes the config file and lists what
// is left to do. It never asks for API keys or passwords; those stay in
// environment variables.
func runSetup(in *bufio.Reader, out io.Writer, path string) error {
	fmt.Fprintf(out, "Setting up WuZuo. This writes %s.\nAPI keys and database passwords are not stored there; they stay in environment variables.\n\n", path)
	var todo []string

	cfg := &config.Config{}
	def, haveKey := cfg.DefaultChoice(envLookup)
	if !haveKey {
		fmt.Fprintln(out, "No LLM API key is set yet. Pick the model you plan to use; you can set its key afterwards.")
		for _, ch := range cfg.Choices(envLookup) {
			if ch.Source == "builtin" {
				def = ch
				break
			}
		}
	}
	chosen, err := pickModel(in, out, cfg.Choices(envLookup), def, !haveKey)
	if err != nil {
		return err
	}
	if !chosen.Available && chosen.KeyEnv != "" {
		todo = append(todo, fmt.Sprintf("export %s=...        # API key for %s", chosen.KeyEnv, chosen.Name))
	}

	var targets []config.Target
	fmt.Fprintln(out)
	add, err := confirm(in, out, "Add a MySQL server to examine?", true)
	if err != nil {
		return err
	}
	if add {
		name, err := ask(in, out, "  Name", "prod-mysql", targetNameRe, "letters, digits, '.', '_' or '-'")
		if err != nil {
			return err
		}
		desc, err := ask(in, out, "  Description, shown to the model (optional)", "", nil, "")
		if err != nil {
			return err
		}
		defEnv := "WUZUO_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name)) + "_DSN"
		env, err := ask(in, out, "  Environment variable that holds its DSN", defEnv, envNameRe, "upper-case letters, digits and '_'")
		if err != nil {
			return err
		}
		targets = append(targets, config.Target{Name: name, Type: "mysql", Description: desc, DSNEnv: env})
		if os.Getenv(env) == "" {
			todo = append(todo, fmt.Sprintf("export %s='wuzuo:<password>@tcp(<host>:3306)/'   # account: docs/mysql-privileges.sql", env))
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(config.Template(chosen.Name, targets)), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nWrote %s (model %s", path, chosen.Name)
	if len(targets) > 0 {
		fmt.Fprintf(out, ", target %s", targets[0].Name)
	}
	fmt.Fprintln(out, ").")

	fmt.Fprintln(out, "\nNext steps:")
	for _, t := range todo {
		fmt.Fprintf(out, "  %s\n", t)
	}
	if len(todo) > 0 {
		fmt.Fprintln(out, "  (add the export lines to your shell profile to keep them)")
	}
	fmt.Fprintln(out, "  wuzuo toolset check                  # verify model, connectivity and privileges")
	fmt.Fprintln(out, `  wuzuo ask "describe what is wrong"`)
	return nil
}

// ask reads one value; Enter takes def. When re is set the value must match.
func ask(in *bufio.Reader, out io.Writer, label, def string, re *regexp.Regexp, rule string) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		if def != "" {
			fmt.Fprintf(out, "%s [%s]: ", label, def)
		} else {
			fmt.Fprintf(out, "%s: ", label)
		}
		line, err := readLine(in)
		if err != nil {
			return "", err
		}
		if line == "" {
			line = def
		}
		if re == nil || re.MatchString(line) {
			return line, nil
		}
		fmt.Fprintf(out, "  invalid: use %s\n", rule)
	}
	return "", fmt.Errorf("%s: no valid value given", strings.TrimSpace(label))
}
