// SPDX-License-Identifier: Apache-2.0

package evidence

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLockerRecordPersistAndVerify(t *testing.T) {
	dir := t.TempDir()
	l, err := NewLocker(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	password := fakeSecret()
	now := time.Now()
	e1, err := l.Record("mysql_processlist", "prod", json.RawMessage(`{"target":"prod"}`), "conn root:"+password+"@tcp(db:3306)/x", nil, now, now.Add(42*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	e2, _ := l.Record("mysql_replication", "prod", nil, "", errors.New("access denied"), now, now)
	if e1.DurationMS != 42 {
		t.Errorf("duration = %dms, want 42", e1.DurationMS)
	}
	if e1.ID != "E1" || e2.ID != "E2" {
		t.Fatalf("ids = %s %s", e1.ID, e2.ID)
	}
	if strings.Contains(e1.Content, password) {
		t.Fatalf("secret not redacted: %s", e1.Content)
	}
	if got := l.Missing([]string{"E1", "e2", "E9"}); len(got) != 1 || got[0] != "E9" {
		t.Fatalf("Missing = %v", got)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.All()) != 2 {
		t.Fatalf("loaded %d items", len(loaded.All()))
	}

	// Tampering must be detected.
	path := filepath.Join(dir, "E1.json")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "conn", "CONN", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected integrity failure")
	}
}

func TestExcerpt(t *testing.T) {
	e := &Evidence{ID: "E3", Content: strings.Repeat("a", 100) + strings.Repeat("z", 100)}
	got := e.Excerpt(40)
	if !strings.HasPrefix(got, strings.Repeat("a", 30)) || !strings.HasSuffix(got, strings.Repeat("z", 10)) || !strings.Contains(got, "sealed as E3") {
		t.Fatalf("unexpected excerpt: %q", got)
	}
	if e.Excerpt(0) != e.Content {
		t.Fatal("max=0 should return everything")
	}
}

func fakeSecret() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "fake" + hex.EncodeToString(b)
}
