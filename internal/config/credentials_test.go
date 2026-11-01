// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialsSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "home", "credentials")
	if err := SaveCredential(path, "WUZUO_TEST_KEY_A", "sk-one"); err != nil {
		t.Fatal(err)
	}
	if err := SaveCredential(path, "WUZUO_TEST_DSN_B", "u:p@tcp(h:3306)/"); err != nil {
		t.Fatal(err)
	}
	if err := SaveCredential(path, "WUZUO_TEST_KEY_A", "sk-two"); err != nil { // replace
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", st.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if strings.Count(string(data), "WUZUO_TEST_KEY_A=") != 1 || !strings.Contains(string(data), "# WuZuo secrets") {
		t.Fatalf("file:\n%s", data)
	}

	t.Setenv("WUZUO_TEST_KEY_A", "")
	t.Setenv("WUZUO_TEST_DSN_B", "from-env") // the environment wins
	loaded, warning, err := LoadCredentials(path)
	if err != nil || warning != "" {
		t.Fatalf("%v %q", err, warning)
	}
	if len(loaded) != 1 || loaded[0] != "WUZUO_TEST_KEY_A" || os.Getenv("WUZUO_TEST_KEY_A") != "sk-two" || os.Getenv("WUZUO_TEST_DSN_B") != "from-env" {
		t.Fatalf("loaded=%v a=%q b=%q", loaded, os.Getenv("WUZUO_TEST_KEY_A"), os.Getenv("WUZUO_TEST_DSN_B"))
	}
}

func TestCredentialsWarnOnOpenPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(path, []byte("# c\nexport WUZUO_TEST_KEY_C=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WUZUO_TEST_KEY_C", "")
	_, warning, err := LoadCredentials(path)
	if err != nil || !strings.Contains(warning, "chmod 600") {
		t.Fatalf("%v %q", err, warning)
	}
	if os.Getenv("WUZUO_TEST_KEY_C") != "x" {
		t.Fatal("export prefix not accepted")
	}
}

func TestCredentialsRejectBadInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	if err := SaveCredential(path, "lower", "x"); err == nil {
		t.Error("lower-case name accepted")
	}
	if err := SaveCredential(path, "OK", "a\nB=c"); err == nil {
		t.Error("multi-line value accepted")
	}
	if err := os.WriteFile(path, []byte("garbage line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadCredentials(path); err == nil {
		t.Error("malformed file accepted")
	}
	if _, _, err := LoadCredentials(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Errorf("missing file: %v", err)
	}
}

func TestMask(t *testing.T) {
	if got := Mask("sk-ant-api03-abcdefghijkl"); got != "sk-ant…ijkl" {
		t.Errorf("Mask = %q", got)
	}
	if got := Mask("short"); got != "*****" {
		t.Errorf("Mask short = %q", got)
	}
}
