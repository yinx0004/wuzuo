// SPDX-License-Identifier: Apache-2.0

package report

import (
	"strings"
	"testing"
	"time"

	"github.com/yinx0004/wuzuo/internal/evidence"
)

type fakeLocker map[string]bool

func (f fakeLocker) Missing(ids []string) []string {
	var m []string
	for _, id := range ids {
		if !f[id] {
			m = append(m, id)
		}
	}
	return m
}

func TestValidate(t *testing.T) {
	ev := fakeLocker{"E1": true, "E2": true}
	good := &Autopsy{
		Summary:      "Row lock contention on orders.",
		CauseOfDeath: Finding{Statement: "Long transaction holds row lock", Confidence: "high", Evidence: []string{"E1"}},
		RuledOut:     []RuledOut{{Hypothesis: "Replication lag", Reason: "Lag is 0s", Evidence: []string{"E2"}}},
		Recommendations: []Action{
			{Description: "Kill blocking session after confirming with owner", Risk: "medium"},
		},
	}
	if p := good.Validate(ev); len(p) != 0 {
		t.Fatalf("unexpected problems: %v", p)
	}

	bad := &Autopsy{
		CauseOfDeath: Finding{Statement: "Disk full", Confidence: "high"},
		RuledOut:     []RuledOut{{Hypothesis: "Lock", Reason: "nah"}},
		Timeline:     []Event{{Time: "10:00", Description: "spike", Evidence: []string{"E7"}}},
	}
	p := strings.Join(bad.Validate(ev), "\n")
	for _, want := range []string{"summary is empty", "cites no evidence", "can only be ruled out with evidence", "unknown evidence E7"} {
		if !strings.Contains(p, want) {
			t.Errorf("missing problem %q in:\n%s", want, p)
		}
	}

	unconfirmed := &Autopsy{Summary: "s", CauseOfDeath: Finding{Statement: "unknown", Confidence: "low"}}
	if p := unconfirmed.Validate(ev); len(p) != 0 {
		t.Fatalf("low-confidence unconfirmed cause should be allowed: %v", p)
	}
}

func TestMarkdown(t *testing.T) {
	a := &Autopsy{
		Title:        "orders 库写入阻塞",
		Summary:      "长事务持有行锁。",
		CauseOfDeath: Finding{Statement: "会话 42 未提交事务", Confidence: "high", Evidence: []string{"E1"}},
	}
	now := time.Now()
	items := []*evidence.Evidence{{ID: "E1", Tool: "mysql_lock_waits", Target: "prod", CollectedAt: now, SHA256: "abcdef0123456789"}}
	md := Markdown(a, Meta{CaseID: "c1", Question: "为什么写不进去", Started: now, Finished: now}, items, DetectLang("为什么"))
	for _, want := range []string{"# orders 库写入阻塞", "死因（根因）", "置信度: 高", "`[E1]`", "| E1 | `mysql_lock_waits`", "abcdef012345"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}
