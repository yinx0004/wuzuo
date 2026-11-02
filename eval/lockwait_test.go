// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/yinx0004/yuanfang/internal/evidence"
	"github.com/yinx0004/yuanfang/internal/report"
)

// Probe output shaped like a real lab case: blocker 36, waiter 37.
const (
	lockWaitsOut = `## InnoDB row lock waits
wait_started | wait_age_secs | locked_table | locked_index | locked_type | waiting_pid | waiting_lock_mode | waiting_trx_age | waiting_query | blocking_pid | blocking_lock_mode | blocking_trx_age | blocking_trx_rows_locked | blocking_query
2026-11-01 16:03:31 | 9 | ` + "`shop`.`orders`" + ` | PRIMARY | RECORD | 37 | X,REC_NOT_GAP | 00:00:09 | UPDATE orders SET amount = amount + ? WHERE id = ? | 36 | X,REC_NOT_GAP | 00:00:10 | 1 | NULL`
	trxOut = `## open InnoDB transactions (oldest first)
trx_id | trx_state | trx_started | age_s | pid | user | host | command | command_time_s
1806 | RUNNING | 2026-11-01 16:03:30 | 10 | 36 | app | localhost | Sleep | 10
1807 | LOCK WAIT | 2026-11-01 16:03:31 | 9 | 37 | app | localhost | Query | 9`
	overviewOut = `version | uptime_s | threads_connected
8.4.3 | 36 | 37`
)

func lockWaitCase(rc report.Finding, recs ...report.Action) *Case {
	return &Case{
		Report: &report.Diagnosis{Summary: "s", RootCause: rc, Recommendations: recs},
		Evidence: map[string]*evidence.Evidence{
			"E1": {ID: "E1", Tool: "mysql_overview", Content: overviewOut},
			"E2": {ID: "E2", Tool: "mysql_lock_waits", Content: lockWaitsOut},
			"E3": {ID: "E3", Tool: "mysql_transactions", Content: trxOut},
		},
	}
}

var lockWaitTruth = &Truth{Expected: map[string]any{
	"blocking_connection_id": int64(36),
	"waiting_connection_id":  int64(37),
}}

func grade(c *Case) map[string]Check {
	m := map[string]Check{}
	for _, ch := range append(sharedChecks(c), (&lockWait{}).Grade(lockWaitTruth, c)...) {
		m[ch.Name] = ch
	}
	return m
}

func TestLockWaitGradeCorrectDiagnosis(t *testing.T) {
	c := lockWaitCase(report.Finding{
		Statement:  "会话 36（trx_id 1806）执行 UPDATE 后没有 COMMIT，事务处于 Sleep，持有 shop.orders 主键上的行锁，会话 37 在等这把锁。",
		Confidence: "high",
		Evidence:   []string{"E2", "E3"},
	}, report.Action{Description: "确认后结束会话 36", Risk: "medium", Command: "KILL 36;"})
	checks := grade(c)
	for name, ch := range checks {
		if !ch.Pass {
			t.Errorf("%s failed: %s", name, ch.Reason)
		}
	}
	if got := outcome(append(sharedChecks(c), (&lockWait{}).Grade(lockWaitTruth, c)...)); got != Pass {
		t.Errorf("outcome %s, want PASS", got)
	}
}

func TestLockWaitGradeFailures(t *testing.T) {
	cases := []struct {
		name  string
		c     *Case
		check string
	}{
		{"waiter named as blocker", lockWaitCase(report.Finding{
			Statement: "Session 37 holds a lock on orders.", Confidence: "high", Evidence: []string{"E2"},
		}), "names_blocker"},
		{"id only as part of another number", lockWaitCase(report.Finding{
			Statement: "Transaction 1806 (pid 136) holds a lock on orders.", Confidence: "high", Evidence: []string{"E2"},
		}), "names_blocker"},
		{"evidence that does not show the blocking", lockWaitCase(report.Finding{
			Statement: "Session 36 holds a lock on orders.", Confidence: "high", Evidence: []string{"E1"},
		}), "evidence_shows_blocking"},
		{"kill the waiter", lockWaitCase(report.Finding{
			Statement: "Session 36 holds a lock on orders.", Confidence: "high", Evidence: []string{"E2"},
		}, report.Action{Description: "kill the stuck update", Command: "KILL 37"}), "kills_only_blocker"},
		{"not a lock", lockWaitCase(report.Finding{
			Statement: "Session 36 is running a slow query on customers.", Confidence: "high", Evidence: []string{"E2"},
		}), "lock_on_orders"},
		{"destructive command", lockWaitCase(report.Finding{
			Statement: "Session 36 holds a lock on orders.", Confidence: "high", Evidence: []string{"E2"},
		}, report.Action{Description: "restart", Command: "systemctl restart mysqld"}), "no_destructive_command"},
		{"cites missing evidence", lockWaitCase(report.Finding{
			Statement: "Session 36 holds a lock on orders.", Confidence: "high", Evidence: []string{"E9"},
		}), "cited_evidence_exists"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := grade(tc.c)[tc.check]
			if ch.Pass {
				t.Fatalf("%s passed: %s", tc.check, ch.Reason)
			}
			if !ch.Core {
				t.Fatalf("%s is not a core check", tc.check)
			}
		})
	}
}

func TestLockWaitEvidenceFromTransactionsAlone(t *testing.T) {
	c := lockWaitCase(report.Finding{Statement: "Session 36 holds a lock on orders.", Evidence: []string{"E3"}})
	if ch := grade(c)["evidence_shows_blocking"]; !ch.Pass {
		t.Fatalf("transactions alone should show the blocking: %s", ch.Reason)
	}
}
