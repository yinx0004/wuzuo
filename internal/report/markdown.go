// SPDX-License-Identifier: Apache-2.0

package report

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/yinx0004/wuzuo/internal/evidence"
)

// Meta describes the case a report belongs to.
type Meta struct {
	CaseID   string
	Question string
	Targets  []string
	Provider string
	Model    string
	Started  time.Time
	Finished time.Time
	Steps    int
	Tokens   int64
}

type labels struct {
	title, caseLbl, question, targets, model, duration, evidenceN,
	summary, cause, confidence, contributing, ruledOut, timeline,
	recommendations, risk, openQ, locker, colID, colTool, colTarget,
	colTime, colStatus, colSeal, ok, failed, footer string
	conf map[string]string
}

var en = labels{
	title: "Autopsy Report", caseLbl: "Case", question: "Complaint", targets: "Examined",
	model: "Examiner", duration: "Duration", evidenceN: "Evidence",
	summary: "Summary", cause: "Cause of Death", confidence: "confidence",
	contributing: "Contributing Factors", ruledOut: "Ruled Out", timeline: "Timeline",
	recommendations: "Recommendations", risk: "risk", openQ: "Open Questions",
	locker: "Evidence Locker", colID: "ID", colTool: "Probe", colTarget: "Target",
	colTime: "Collected (UTC)", colStatus: "Status", colSeal: "Seal", ok: "ok", failed: "failed",
	footer: "WuZuo does not execute recommendations. Review them before acting.",
	conf:   map[string]string{"high": "high", "medium": "medium", "low": "low"},
}

var zh = labels{
	title: "尸格 · 验尸报告", caseLbl: "案号", question: "报案", targets: "勘验对象",
	model: "仵作", duration: "用时", evidenceN: "证物",
	summary: "案情摘要", cause: "死因（根因）", confidence: "置信度",
	contributing: "诱因", ruledOut: "已排除", timeline: "时间线",
	recommendations: "处置建议", risk: "风险", openQ: "存疑",
	locker: "证物袋", colID: "编号", colTool: "勘验手段", colTarget: "对象",
	colTime: "采集时间 (UTC)", colStatus: "状态", colSeal: "封签", ok: "成功", failed: "失败",
	footer: "WuZuo 不会执行处置建议，操作前请人工复核。",
	conf:   map[string]string{"high": "高", "medium": "中", "low": "低"},
}

// DetectLang returns "zh" if s contains Han characters, else "en".
func DetectLang(s string) string {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return "zh"
		}
	}
	return "en"
}

// Markdown renders the report for humans.
func Markdown(a *Autopsy, m Meta, items []*evidence.Evidence, lang string) string {
	l := en
	if lang == "zh" {
		l = zh
	}
	var b strings.Builder
	title := a.Title
	if title == "" {
		title = l.title
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "> **%s** · `%s`  \n", l.title, m.CaseID)
	fmt.Fprintf(&b, "> **%s**: %s  \n", l.question, m.Question)
	if len(m.Targets) > 0 {
		fmt.Fprintf(&b, "> **%s**: %s  \n", l.targets, strings.Join(m.Targets, ", "))
	}
	fmt.Fprintf(&b, "> **%s**: %s/%s · **%s**: %s · **%s**: %d\n\n",
		l.model, m.Provider, m.Model, l.duration, m.Finished.Sub(m.Started).Round(time.Second), l.evidenceN, len(items))

	fmt.Fprintf(&b, "## %s\n\n%s\n\n", l.summary, a.Summary)

	fmt.Fprintf(&b, "## %s\n\n", l.cause)
	fmt.Fprintf(&b, "%s\n\n", a.CauseOfDeath.Statement)
	fmt.Fprintf(&b, "*%s: %s* %s\n\n", l.confidence, l.conf[a.CauseOfDeath.Confidence], cite(a.CauseOfDeath.Evidence))

	if len(a.Contributing) > 0 {
		fmt.Fprintf(&b, "## %s\n\n", l.contributing)
		for _, f := range a.Contributing {
			fmt.Fprintf(&b, "- %s *(%s: %s)* %s\n", f.Statement, l.confidence, l.conf[f.Confidence], cite(f.Evidence))
		}
		b.WriteString("\n")
	}

	if len(a.RuledOut) > 0 {
		fmt.Fprintf(&b, "## %s\n\n", l.ruledOut)
		for _, r := range a.RuledOut {
			fmt.Fprintf(&b, "- ~~%s~~: %s %s\n", r.Hypothesis, r.Reason, cite(r.Evidence))
		}
		b.WriteString("\n")
	}

	if len(a.Timeline) > 0 {
		fmt.Fprintf(&b, "## %s\n\n", l.timeline)
		for _, e := range a.Timeline {
			fmt.Fprintf(&b, "- **%s** %s %s\n", e.Time, e.Description, cite(e.Evidence))
		}
		b.WriteString("\n")
	}

	if len(a.Recommendations) > 0 {
		fmt.Fprintf(&b, "## %s\n\n", l.recommendations)
		for i, r := range a.Recommendations {
			fmt.Fprintf(&b, "%d. %s *(%s: %s)*\n", i+1, r.Description, l.risk, l.conf[r.Risk])
			if r.Command != "" {
				fmt.Fprintf(&b, "   ```\n   %s\n   ```\n", strings.ReplaceAll(r.Command, "\n", "\n   "))
			}
		}
		b.WriteString("\n")
	}

	if len(a.OpenQuestions) > 0 {
		fmt.Fprintf(&b, "## %s\n\n", l.openQ)
		for _, q := range a.OpenQuestions {
			fmt.Fprintf(&b, "- %s\n", q)
		}
		b.WriteString("\n")
	}

	if len(items) > 0 {
		fmt.Fprintf(&b, "## %s\n\n", l.locker)
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n|---|---|---|---|---|---|\n",
			l.colID, l.colTool, l.colTarget, l.colTime, l.colStatus, l.colSeal)
		for _, e := range items {
			status := l.ok
			if e.Error != "" {
				status = l.failed
			}
			fmt.Fprintf(&b, "| %s | `%s` | %s | %s | %s | `%s` |\n",
				e.ID, e.Tool, e.Target, e.CollectedAt.Format("2006-01-02 15:04:05"), status, short(e.SHA256))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "---\n*%s*\n", l.footer)
	return b.String()
}

func cite(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return "`[" + strings.Join(ids, ", ") + "]`"
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
