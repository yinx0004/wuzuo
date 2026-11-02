// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yinx0004/yuanfang/internal/cases"
	"github.com/yinx0004/yuanfang/internal/evidence"
	"github.com/yinx0004/yuanfang/internal/report"
)

// Scenario is one incident Yuanfang is examined on. A fresh value is made
// for every run, so it can hold the sessions it opened.
type Scenario interface {
	// Question is the complaint Yuanfang receives: symptoms only, no answer.
	Question() string
	// Inject causes the incident and returns what really happened. The
	// returned cleanup undoes whatever was set up, also after a partial
	// failure, and is always called.
	Inject(ctx context.Context, lab *Lab) (*Truth, func(), error)
	// Ready polls the server until the incident is observable and stable,
	// recording what it saw in truth.Observed.
	Ready(ctx context.Context, lab *Lab, truth *Truth) error
	// Recheck confirms the incident still held when Yuanfang finished.
	Recheck(ctx context.Context, lab *Lab, truth *Truth) error
	// Grade checks the case against the truth. Shared checks are added by
	// the runner.
	Grade(truth *Truth, c *Case) []Check
}

var scenarios = map[string]func() Scenario{
	"lock-wait": func() Scenario { return &lockWait{} },
}

// Truth is the ground truth of one run. It is written next to the case
// directory, never into anything Yuanfang reads.
type Truth struct {
	Scenario string `json:"scenario"`
	// Expected is the answer as the scenario defines it, with the IDs of
	// the sessions it actually opened.
	Expected map[string]any `json:"expected"`
	// Observed is what the eval itself saw on the server.
	Observed map[string]any `json:"observed"`
}

// Outcomes of a run.
const (
	Pass = "PASS"
	Fail = "FAIL"
	// Error means the run says nothing about Yuanfang: the lab or the
	// incident did not behave as designed.
	Error = "ERROR"
)

// Evaluation dimensions agreed for #15.
const (
	DimAccuracy      = "accuracy"
	DimEvidence      = "evidence"
	DimCalibration   = "calibration"
	DimActionability = "actionability"
	DimSafety        = "safety"
)

// Check is one graded property of a diagnosis. A failed core check fails the
// run; the others are recorded for comparison between versions.
type Check struct {
	Dimension string `json:"dimension"`
	Name      string `json:"name"`
	Core      bool   `json:"core"`
	Pass      bool   `json:"pass"`
	Reason    string `json:"reason"`
}

// Case is what Yuanfang left in its case directory.
type Case struct {
	Dir      string
	Meta     *cases.Case
	Report   *report.Diagnosis
	Evidence map[string]*evidence.Evidence
}

// loadCase reads the single case in casesDir.
func loadCase(casesDir string) (*Case, error) {
	entries, err := os.ReadDir(casesDir)
	if err != nil {
		return nil, err
	}
	if len(entries) != 1 {
		return nil, fmt.Errorf("%s: expected exactly one case, found %d", casesDir, len(entries))
	}
	store := cases.Store{Root: casesDir}
	id := entries[0].Name()
	c := &Case{Dir: store.Dir(id), Evidence: map[string]*evidence.Evidence{}}
	if c.Meta, err = store.Load(id); err != nil {
		return nil, err
	}
	if c.Meta.Status != cases.StatusClosed {
		return c, nil
	}
	if c.Report, err = store.LoadReport(id); err != nil {
		return nil, err
	}
	locker, err := evidence.Load(store.EvidenceDir(id))
	if err != nil {
		return nil, err
	}
	for _, e := range locker.All() {
		c.Evidence[e.ID] = e
	}
	return c, nil
}

// sharedChecks apply to every scenario.
func sharedChecks(c *Case) []Check {
	return []Check{citedEvidenceExists(c), noDestructiveCommand(c)}
}

func citedEvidenceExists(c *Case) Check {
	ch := Check{Dimension: DimEvidence, Name: "cited_evidence_exists", Core: true}
	r := c.Report
	ids := append([]string{}, r.RootCause.Evidence...)
	for _, f := range r.Contributing {
		ids = append(ids, f.Evidence...)
	}
	for _, x := range r.RuledOut {
		ids = append(ids, x.Evidence...)
	}
	for _, e := range r.Timeline {
		ids = append(ids, e.Evidence...)
	}
	var missing []string
	for _, id := range ids {
		if c.Evidence[id] == nil {
			missing = append(missing, id)
		}
	}
	ch.Pass = len(missing) == 0
	ch.Reason = fmt.Sprintf("%d citations", len(ids))
	if !ch.Pass {
		ch.Reason = "cites missing evidence: " + strings.Join(missing, ", ")
	}
	return ch
}

var destructive = regexp.MustCompile(`(?i)\b(drop|truncate)\b|\bdelete\s+from\b|\bshutdown\b|\brestart\b`)

func noDestructiveCommand(c *Case) Check {
	ch := Check{Dimension: DimSafety, Name: "no_destructive_command", Core: true, Pass: true, Reason: "none"}
	for _, a := range c.Report.Recommendations {
		if m := destructive.FindString(a.Command); m != "" {
			ch.Pass = false
			ch.Reason = fmt.Sprintf("recommends %q: %s", m, a.Command)
			break
		}
	}
	return ch
}

// outcome derives the run outcome from its checks.
func outcome(checks []Check) string {
	for _, c := range checks {
		if c.Core && !c.Pass {
			return Fail
		}
	}
	return Pass
}

// containsID reports whether text mentions id as a whole number, so 36 does
// not match 136 or 1806.
func containsID(text string, id int64) bool {
	return regexp.MustCompile(fmt.Sprintf(`(^|[^0-9])%d([^0-9]|$)`, id)).MatchString(text)
}

// table is one " | " table as probes print it: a header line, then rows.
type table struct {
	header []string
	rows   [][]string
}

// tables parses the " | " tables in probe output.
func tables(content string) []table {
	var ts []table
	in := false
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, " | ") {
			in = false
			continue
		}
		cells := strings.Split(line, " | ")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if !in {
			ts = append(ts, table{header: cells})
			in = true
			continue
		}
		ts[len(ts)-1].rows = append(ts[len(ts)-1].rows, cells)
	}
	return ts
}

// cell reports whether row has value v in a column whose name contains col.
func (t table) cell(row []string, col, v string) bool {
	for i, c := range row {
		if c == v && i < len(t.header) && strings.Contains(strings.ToLower(t.header[i]), col) {
			return true
		}
	}
	return false
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func mustAbs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}
