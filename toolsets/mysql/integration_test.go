// SPDX-License-Identifier: Apache-2.0

//go:build integration

package mysql

// Run against the lab:  make lab-up && make test-integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func labToolset(t *testing.T) *Toolset {
	t.Helper()
	dsn := os.Getenv("YUANFANG_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("YUANFANG_TEST_MYSQL_DSN not set")
	}
	ts, err := New(Options{Targets: []Target{{Name: "lab", DSN: dsn}}, QueryTimeout: 10 * time.Second, Redact: true, AllowExplain: true, AllowRawSQL: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ts.Close() })
	return ts
}

func TestIntegrationCheck(t *testing.T) {
	ts := labToolset(t)
	for _, r := range ts.Check(context.Background()) {
		if !r.OK {
			t.Errorf("check %s failed: %s", r.Item, r.Detail)
		}
	}
}

func TestIntegrationEveryProbeRuns(t *testing.T) {
	ts := labToolset(t)
	args := map[string]string{
		"mysql_status_delta": `{"interval_seconds":1}`,
		"mysql_table_stats":  `{"schema":"shop","table":"orders"}`,
		"mysql_explain":      `{"schema":"shop","statement":"SELECT * FROM orders WHERE customer_id = 7"}`,
		"mysql_variables":    `{"pattern":"innodb_lock%"}`,
		"mysql_query":        `{"statement":"SELECT COUNT(*) AS n FROM performance_schema.threads"}`,
	}
	for _, tool := range ts.Tools() {
		name := tool.Spec().Name
		t.Run(name, func(t *testing.T) {
			raw := args[name]
			if raw == "" {
				raw = "{}"
			}
			res, err := tool.Run(context.Background(), json.RawMessage(raw))
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if strings.TrimSpace(res.Content) == "" || res.Target != "lab" {
				t.Fatalf("%s: empty result %+v", name, res)
			}
		})
	}
}

func TestIntegrationSessionIsReadOnly(t *testing.T) {
	dsn := os.Getenv("YUANFANG_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("YUANFANG_TEST_MYSQL_DSN not set")
	}
	db, err := open(dsn, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE shop.orders SET amount = amount WHERE id = 1"); err == nil {
		t.Fatal("write succeeded on a Yuanfang session")
	}
	var ro int
	if err := db.QueryRow("SELECT @@transaction_read_only").Scan(&ro); err != nil || ro != 1 {
		t.Fatalf("transaction_read_only = %d, %v", ro, err)
	}
}

// TestIntegrationServerLayersHoldWithoutGuard calls guardedRun directly with
// statements the guard would reject, using an account that has every
// privilege, proving the session and transaction layers stop writes on their
// own even if both the guard and least-privilege grants were missing.
func TestIntegrationServerLayersHoldWithoutGuard(t *testing.T) {
	dsn := os.Getenv("YUANFANG_TEST_MYSQL_ROOT_DSN")
	if dsn == "" {
		t.Skip("YUANFANG_TEST_MYSQL_ROOT_DSN not set")
	}
	db, err := open(dsn, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	attacks := []string{
		"UPDATE orders SET amount = 0 WHERE id = 1",
		"SELECT 1; UPDATE orders SET amount = 0 WHERE id = 1",
		"CREATE TABLE pwned (id INT)",
	}
	for _, stmt := range attacks {
		err := guardedRun(ctx, db, "shop", func(conn *sql.Conn) error {
			_, _, _, err := queryLimit(ctx, conn, stmt, 1)
			return err
		})
		if err == nil {
			t.Errorf("server accepted %q", stmt)
			continue
		}
		t.Logf("%q rejected: %v", stmt, err)
	}
	var amount string
	if err := db.QueryRowContext(ctx, "SELECT amount FROM shop.orders WHERE id = 1").Scan(&amount); err != nil || amount == "0.00" {
		t.Fatalf("row was modified: %s %v", amount, err)
	}
}

func TestIntegrationRawQueryRowCap(t *testing.T) {
	ts := labToolset(t)
	for _, tool := range ts.Tools() {
		if tool.Spec().Name != "mysql_query" {
			continue
		}
		res, err := tool.Run(context.Background(), json.RawMessage(`{"statement":"SELECT id FROM shop.orders"}`))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res.Content, "first 200 rows") || !strings.Contains(res.Content, "(200 rows)") {
			t.Fatalf("row cap not applied:\n%s", toolsetClipForTest(res.Content))
		}
	}
}

func toolsetClipForTest(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
