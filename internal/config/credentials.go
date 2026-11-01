// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The credentials file keeps secrets out of config.yaml: API keys and DSNs
// entered during setup are stored as KEY=VALUE lines in $WUZUO_HOME/credentials
// (mode 0600) and exported into the process environment at startup. A
// variable already set in the environment always wins, so `export` keeps
// working and overrides the file.

var credentialKeyRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

const storeBanner = `# WuZuo secrets: API keys and database DSNs, one KEY=VALUE per line.
# Keep this file private (mode 0600). Environment variables with the same
# name take precedence over the values here.
`

// CredentialsPath returns $WUZUO_HOME/credentials.
func CredentialsPath() string {
	return filepath.Join(Home(), "credentials")
}

// LoadCredentials exports every variable in the file that is not already set
// (non-empty) in the environment. It returns the names it exported and a
// warning when the file is readable by other users. A missing file is not an
// error.
func LoadCredentials(path string) (loaded []string, warning string, err error) {
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if st.Mode().Perm()&0o077 != 0 {
		warning = fmt.Sprintf("%s is accessible by other users (mode %v); run: chmod 600 %s", path, st.Mode().Perm(), path)
	}
	data, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec // fixed path under WUZUO_HOME
	if err != nil {
		return nil, warning, err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		key, value, ok, err := parseCredentialLine(sc.Text())
		if err != nil {
			return loaded, warning, fmt.Errorf("%s line %d: %w", path, n, err)
		}
		if !ok || os.Getenv(key) != "" {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return loaded, warning, err
		}
		loaded = append(loaded, key)
	}
	return loaded, warning, sc.Err()
}

// SaveCredential sets key=value in the credentials file, creating it with mode
// 0600 if needed. An existing line for key is replaced; other lines and
// comments are kept.
func SaveCredential(path, key, value string) error {
	if !credentialKeyRe.MatchString(key) {
		return fmt.Errorf("invalid variable name %q", key)
	}
	if strings.ContainsAny(value, "\r\n") {
		return errors.New("value must be a single line")
	}
	var lines []string
	data, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec // fixed path under WUZUO_HOME
	switch {
	case errors.Is(err, os.ErrNotExist):
		lines = strings.Split(strings.TrimRight(storeBanner, "\n"), "\n")
	case err != nil:
		return err
	default:
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	}
	replaced := false
	for i, l := range lines {
		if k, _, ok, _ := parseCredentialLine(l); ok && k == key {
			lines[i] = key + "=" + value
			replaced = true
		}
	}
	if !replaced {
		lines = append(lines, key+"="+value)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	path = filepath.Clean(path)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil { //nolint:gosec // fixed path under WUZUO_HOME
		return err
	}
	return os.Rename(tmp, path)
}

func parseCredentialLine(line string) (key, value string, ok bool, err error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false, nil
	}
	line = strings.TrimPrefix(line, "export ")
	key, value, found := strings.Cut(line, "=")
	key = strings.TrimSpace(key)
	if !found || !credentialKeyRe.MatchString(key) {
		return "", "", false, errors.New("expected KEY=VALUE")
	}
	return key, strings.TrimSpace(value), true, nil
}

// Mask shows the start and end of a secret, for confirming what was saved.
func Mask(secret string) string {
	r := []rune(secret)
	if len(r) <= 10 {
		return strings.Repeat("*", len(r))
	}
	return string(r[:6]) + "…" + string(r[len(r)-4:])
}
