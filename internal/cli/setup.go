// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"golang.org/x/term"

	"github.com/yinx0004/wuzuo/internal/config"
	"github.com/yinx0004/wuzuo/toolsets/mysql"
)

var targetNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// fromCredentials records which variables this run took from the credentials
// file (as opposed to the user's shell), so setup can describe and replace them.
var fromCredentials = map[string]bool{}

// existingSecret explains where an already-set variable comes from and, for
// values from the credentials file, asks whether to replace it. It returns
// true when setup should ask for a new value.
func existingSecret(in *bufio.Reader, out io.Writer, indent, name, shown, credPath string) (bool, error) {
	if !fromCredentials[name] {
		fmt.Fprintf(out, "%sUsing %s from your shell environment (export); it overrides %s.\n", indent, name, displayPath(credPath))
		return false, nil
	}
	return confirm(in, out, fmt.Sprintf("%s%s is already saved in %s (%s). Replace it?", indent, name, displayPath(credPath), shown), false)
}

// runSetup walks through first-time configuration in a terminal: the default
// model and its API key, optionally a MySQL target and its DSN. Settings go to
// the config file; the key and DSN go to the credentials file (mode 0600), so
// config.yaml never holds secrets. Anything skipped is listed at the end.
func runSetup(in *bufio.Reader, out io.Writer, path string) error {
	credPath := config.CredentialsPath()
	fmt.Fprintf(out, "Setting up WuZuo.\n  settings: %s\n  secrets:  %s (mode 0600)\n\n", displayPath(path), displayPath(credPath))
	var todo, savedSecrets []string
	credExisted, cfgExisted := fileExists(credPath), fileExists(path)

	// 1. Model.
	cfg := &config.Config{}
	def, haveKey := cfg.DefaultChoice(envLookup)
	if !haveKey {
		fmt.Fprintln(out, "No LLM API key is set yet. Pick the model you plan to use; you can enter its key next.")
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

	// 2. Its API key.
	if keyEnv := chosen.KeyEnv; keyEnv != "" {
		replace := os.Getenv(keyEnv) == ""
		if !replace {
			if replace, err = existingSecret(in, out, "", keyEnv, config.Mask(os.Getenv(keyEnv)), credPath); err != nil {
				return err
			}
		}
		if replace {
			fmt.Fprintf(out, "\nPaste the API key for %s, saved as %s (input hidden; Enter to skip):\n> ", chosen.Name, keyEnv)
			key, err := readSecret(in, out)
			if err != nil {
				return err
			}
			if key == "" {
				todo = append(todo, fmt.Sprintf("export %s=...        # API key for %s", keyEnv, chosen.Name))
			} else {
				if err := saveSecret(credPath, keyEnv, key); err != nil {
					return err
				}
				fmt.Fprintf(out, "Saved %s to %s (%s).\n", keyEnv, displayPath(credPath), config.Mask(key))
				savedSecrets = append(savedSecrets, keyEnv)
			}
		}
	}

	// 3. A MySQL target and its DSN.
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
		env := "WUZUO_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name)) + "_DSN"
		targets = append(targets, config.Target{Name: name, Type: "mysql", Description: desc, DSNEnv: env})
		replace := os.Getenv(env) == ""
		if !replace {
			shown := "set"
			if c, err := driver.ParseDSN(os.Getenv(env)); err == nil {
				shown = c.User + "@" + c.Addr
			}
			if replace, err = existingSecret(in, out, "  ", env, shown, credPath); err != nil {
				return err
			}
		}
		if replace {
			dsn, summary, err := askMySQLConnection(in, out)
			if err != nil {
				return err
			}
			if dsn == "" {
				todo = append(todo, fmt.Sprintf("export %s='user:password@tcp(host:3306)/'   # account: docs/mysql-privileges.sql", env))
			} else {
				if err := saveSecret(credPath, env, dsn); err != nil {
					return err
				}
				fmt.Fprintf(out, "  Saved %s to %s (%s).\n", env, displayPath(credPath), summary)
				savedSecrets = append(savedSecrets, env)
				testConnection(out, name, dsn)
			}
		}
	}

	// 4. Write the config.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(config.Template(chosen.Name, targets)), 0o600); err != nil {
		return err
	}
	settings := "model " + chosen.Name
	if len(targets) > 0 {
		settings += ", target " + targets[0].Name
	}
	fmt.Fprintln(out, "\nFiles written:")
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintf(w, "  %s\t%s: %s\n", displayPath(path), writeVerb(cfgExisted, "overwritten"), settings)
	if len(savedSecrets) > 0 {
		fmt.Fprintf(w, "  %s\t%s: %s\n", displayPath(credPath), writeVerb(credExisted, "updated"), strings.Join(savedSecrets, ", "))
	}
	_ = w.Flush()

	fmt.Fprintln(out, "\nNext steps:")
	for _, t := range todo {
		fmt.Fprintf(out, "  %s\n", t)
	}
	if len(todo) > 0 {
		fmt.Fprintf(out, "  (or run `wuzuo init --force` again to enter them; values go to %s)\n", displayPath(credPath))
	}
	fmt.Fprintln(out, "  wuzuo toolset check                  # verify model, connectivity and privileges")
	fmt.Fprintln(out, `  wuzuo ask "describe what is wrong"`)
	return nil
}

var (
	hostRe = regexp.MustCompile(`^[A-Za-z0-9._:%-]+$`) // names, IPv4, IPv6
	portRe = regexp.MustCompile(`^([1-9][0-9]{0,3}|[1-5][0-9]{4}|6[0-4][0-9]{3}|65[0-4][0-9]{2}|655[0-2][0-9]|6553[0-5])$`)
	userRe = regexp.MustCompile(`^[^\s:@/]{1,80}$`)
)

func writeVerb(existed bool, ifExisted string) string {
	if existed {
		return ifExisted
	}
	return "created"
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// displayPath shortens paths under the home directory to ~/...
func displayPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return p
}

// askMySQLConnection asks for host, port, user and password one at a time
// and builds the DSN with the driver, which handles special characters in the
// password. Enter on the host skips. It returns the DSN and a summary without
// the password.
func askMySQLConnection(in *bufio.Reader, out io.Writer) (dsn, summary string, err error) {
	host, err := ask(in, out, "  Host or IP (Enter to skip and set it up later)", "", nil, "")
	if err != nil || host == "" {
		return "", "", err
	}
	for attempt := 0; !hostRe.MatchString(host); attempt++ {
		if attempt == 2 {
			return "", "", fmt.Errorf("invalid host %q", host)
		}
		fmt.Fprintln(out, "  invalid: use a host name or an IP address")
		if host, err = ask(in, out, "  Host or IP", "", nil, ""); err != nil {
			return "", "", err
		}
	}
	port, err := ask(in, out, "  Port", "3306", portRe, "a number from 1 to 65535")
	if err != nil {
		return "", "", err
	}
	user, err := ask(in, out, "  User", "wuzuo", userRe, "a MySQL user name (docs/mysql-privileges.sql creates 'wuzuo')")
	if err != nil {
		return "", "", err
	}
	fmt.Fprint(out, "  Password (input hidden): ")
	password, err := readSecret(in, out)
	if err != nil {
		return "", "", err
	}

	c := driver.NewConfig()
	c.User, c.Passwd, c.Net, c.Addr = user, password, "tcp", net.JoinHostPort(strings.Trim(host, "[]"), port)
	return c.FormatDSN(), fmt.Sprintf("%s@%s", user, c.Addr), nil
}

// testConnection runs the toolset's prerequisite checks against a new target
// and reports the result; failures are shown but do not stop setup.
func testConnection(out io.Writer, name, dsn string) {
	ts, err := mysql.New(mysql.Options{Targets: []mysql.Target{{Name: name, DSN: dsn}}, QueryTimeout: 5 * time.Second})
	if err != nil {
		fmt.Fprintf(out, "  ✗ %v\n", err)
		return
	}
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	results := ts.Check(ctx)
	var failed []string
	version := ""
	for _, r := range results {
		if r.Item == "connect" && r.OK {
			version = r.Detail
		}
		if !r.OK {
			failed = append(failed, r.Item+": "+r.Detail)
		}
	}
	switch {
	case version == "":
		fmt.Fprintf(out, "  ✗ could not connect: %s\n", strings.Join(failed, "; "))
		fmt.Fprintln(out, "    Saved anyway; fix it later and run `wuzuo toolset check`.")
	case len(failed) == 0:
		fmt.Fprintf(out, "  ✓ connected (%s), all checks passed\n", version)
	default:
		fmt.Fprintf(out, "  ✓ connected (%s), but:\n", version)
		for _, f := range failed {
			fmt.Fprintf(out, "    ✗ %s\n", f)
		}
	}
}

// saveSecret stores a secret in the credentials file and in this process's
// environment, so the rest of the run can use it.
func saveSecret(path, key, value string) error {
	if err := config.SaveCredential(path, key, value); err != nil {
		return fmt.Errorf("save %s: %w", key, err)
	}
	return os.Setenv(key, value)
}

// readSecret reads a line without echo when the prompt reads from a terminal,
// and as a plain line otherwise (tests, piped input).
func readSecret(in *bufio.Reader, out io.Writer) (string, error) {
	if f, ok := promptInput.(*os.File); ok && term.IsTerminal(int(f.Fd())) && in.Buffered() == 0 {
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(out)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	return readLine(in)
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
