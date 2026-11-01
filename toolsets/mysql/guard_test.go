// SPDX-License-Identifier: Apache-2.0

package mysql

import "testing"

func TestCheckReadOnly(t *testing.T) {
	allowed := []string{
		"SELECT * FROM performance_schema.threads",
		"select 1;",
		"WITH x AS (SELECT 1) SELECT * FROM x",
		"SHOW ENGINE INNODB STATUS",
		"EXPLAIN SELECT * FROM t WHERE a = 1",
		"  -- comment\nSELECT 'drop table; sleep(1)' AS s",
		"SELECT * FROM performance_schema.threads;  ",
		"DESC orders",
	}
	for _, q := range allowed {
		if err := CheckReadOnly(q); err != nil {
			t.Errorf("should allow %q: %v", q, err)
		}
	}
	denied := []string{
		"DELETE FROM t",
		"UPDATE t SET a = 1",
		"SELECT 1; DROP TABLE t",
		"SELECT * FROM t FOR UPDATE",
		"SELECT SLEEP(10)",
		"SELECT * INTO OUTFILE '/tmp/x' FROM t",
		"EXPLAIN ANALYZE SELECT * FROM t",
		"/*!50000 DROP TABLE t */ SELECT 1",
		"SET GLOBAL read_only = 0",
		"WITH x AS (SELECT 1) DELETE FROM t",
		"KILL 42",
		"",
	}
	for _, q := range denied {
		if err := CheckReadOnly(q); err == nil {
			t.Errorf("should deny %q", q)
		}
	}
}

func TestQuoteIdent(t *testing.T) {
	if q, err := quoteIdent("orders_2024"); err != nil || q != "`orders_2024`" {
		t.Fatalf("got %q %v", q, err)
	}
	for _, bad := range []string{"a`b", "a b", "", "x;drop"} {
		if _, err := quoteIdent(bad); err == nil {
			t.Errorf("should reject %q", bad)
		}
	}
}

func TestCheckReadOnlyCommentAndQuoteTricks(t *testing.T) {
	denied := []string{
		"SELECT '#'; DROP TABLE t",
		"SELECT 1 -- it's\n; DROP TABLE t",
		"SELECT 1 /* x */; DELETE FROM t",
		"SELECT 'unterminated",
		"SELECT /*+ MAX_EXECUTION_TIME(1) */ 1",
		"SELECT 1 /* unterminated",
		// Under NO_BACKSLASH_ESCAPES the server ends the string at the second quote.
		"SELECT '\\'; DELETE FROM t; -- '",
		"SELECT 'it\\'s'",
		"SELECT @x := 1",
	}
	for _, q := range denied {
		if err := CheckReadOnly(q); err == nil {
			t.Errorf("should deny %q", q)
		}
	}
	allowed := []string{
		"SELECT 'a;b' AS x # trailing comment",
		"SELECT \"it\"\"s\", 'it''s' FROM `we;ird`",
		"SELECT 1 /* note; with semicolon */",
	}
	for _, q := range allowed {
		if err := CheckReadOnly(q); err != nil {
			t.Errorf("should allow %q: %v", q, err)
		}
	}
}

func TestGuardReadOnlyReturnsCheckedText(t *testing.T) {
	got, err := GuardReadOnly("  SELECT 1 ;  ")
	if err != nil || got != "SELECT 1" {
		t.Fatalf("got %q %v", got, err)
	}
}
