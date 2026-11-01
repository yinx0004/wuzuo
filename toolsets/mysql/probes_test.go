// SPDX-License-Identifier: Apache-2.0

package mysql

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const innodbSample = `
=====================================
2026-11-01 10:00:00 0x7f INNODB MONITOR OUTPUT
=====================================
------------------------
LATEST DETECTED DEADLOCK
------------------------
*** (1) TRANSACTION:
TRANSACTION 1234, ACTIVE 5 sec starting index read
MySQL thread id 42, OS thread handle 1, query id 9 10.0.0.1 app updating
UPDATE orders SET status = 'paid' WHERE id = 1001
RECORD LOCKS space id 2 page no 4 n bits 72 index PRIMARY of table ` + "`shop`.`orders`" + `
Record lock, heap no 2 PHYSICAL RECORD: n_fields 5; compact format; info bits 0
 0: len 4; hex 800003e9; asc     ;;
 1: len 11; hex 616c6963654065782e636f; asc alice@ex.co;;
------------
TRANSACTIONS
------------
Trx id counter 5000
History list length 12
`

func TestInnoDBStatusSplitAndRedact(t *testing.T) {
	s := splitInnoDBStatus(innodbSample)
	dl, ok := s["LATEST DETECTED DEADLOCK"]
	if !ok {
		t.Fatalf("sections: %v", s)
	}
	if !strings.Contains(s["TRANSACTIONS"], "History list length 12") {
		t.Fatalf("transactions section: %q", s["TRANSACTIONS"])
	}
	ts := &Toolset{opts: Options{Redact: true}}
	body := recordDump.ReplaceAllString(dl, "${1}<redacted>")
	body = sqlLine.ReplaceAllStringFunc(body, func(l string) string { return ts.sqlText(l, 400) })
	if strings.Contains(body, "alice") || strings.Contains(body, "'paid'") {
		t.Fatalf("data leaked:\n%s", body)
	}
	if !strings.Contains(body, "UPDATE orders SET status = ? WHERE id = ?") || !strings.Contains(body, "TRANSACTION 1234") {
		t.Fatalf("over-redacted:\n%s", body)
	}
}

func TestSpecsAreValid(t *testing.T) {
	ts, err := New(Options{Targets: []Target{{Name: "a", DSN: "u@tcp(127.0.0.1:3306)/"}, {Name: "b", DSN: "u@tcp(127.0.0.1:3307)/"}}, AllowExplain: true, AllowRawSQL: true})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range ts.Tools() {
		s := tool.Spec()
		if seen[s.Name] {
			t.Errorf("duplicate %s", s.Name)
		}
		seen[s.Name] = true
		hasTarget := false
		for _, r := range s.Parameters.Required {
			hasTarget = hasTarget || r == "target"
		}
		if !hasTarget {
			t.Errorf("%s: target must be required with multiple targets", s.Name)
		}
	}
	if !seen["mysql_query"] {
		t.Error("mysql_query missing with AllowRawSQL")
	}
	if !seen["mysql_explain"] {
		t.Error("mysql_explain missing with AllowExplain")
	}
}

func TestExplainRefusesWritesWithGuidance(t *testing.T) {
	ts := &Toolset{opts: Options{Redact: true}}
	for _, stmt := range []string{"UPDATE orders SET status = 'x' WHERE customer_id = 7", "delete from orders where id = 1"} {
		raw, _ := json.Marshal(map[string]string{"schema": "shop", "statement": stmt})
		_, err := ts.explain(context.Background(), nil, raw)
		if err == nil || !strings.Contains(err.Error(), "Explain a SELECT with the same FROM/JOIN/WHERE") {
			t.Errorf("%q: %v", stmt, err)
		}
	}
}

func TestModelSQLProbesOffByDefault(t *testing.T) {
	ts, err := New(Options{Targets: []Target{{Name: "a", DSN: "u@tcp(127.0.0.1:3306)/"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range ts.Tools() {
		if n := tool.Spec().Name; n == "mysql_explain" || n == "mysql_query" {
			t.Errorf("%s registered without being enabled", n)
		}
	}
}
