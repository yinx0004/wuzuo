// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yinx0004/yuanfang/internal/cases"
)

// Run is the result of one scenario run.
type Run struct {
	Scenario string        `json:"scenario"`
	Index    int           `json:"index"`
	Outcome  string        `json:"outcome"`
	Error    string        `json:"error,omitempty"`
	CaseID   string        `json:"case_id,omitempty"`
	Model    string        `json:"model,omitempty"`
	Steps    int           `json:"steps,omitempty"`
	Tokens   cases.Tokens  `json:"tokens"`
	Duration time.Duration `json:"duration_ns,omitempty"`
	Checks   []Check       `json:"checks,omitempty"`
}

type runner struct {
	lab     *Lab
	bin     string // the yuanfang binary under test
	model   string
	timeout time.Duration
}

// run executes one scenario end to end. Cleanup runs whatever happens.
func (rn *runner) run(ctx context.Context, name string, index int, dir string) (r Run) {
	r = Run{Scenario: name, Index: index, Outcome: Error}
	fail := func(stage string, err error) Run {
		r.Error = stage + ": " + err.Error()
		return r
	}
	defer func() { _ = writeJSON(filepath.Join(dir, "grading.json"), r) }()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail("setup", err)
	}
	if err := rn.lab.Restart(ctx); err != nil {
		return fail("reset lab", err)
	}
	sc := scenarios[name]()
	truth, cleanup, err := sc.Inject(ctx, rn.lab)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return fail("inject", err)
	}
	truthPath := filepath.Join(dir, "ground-truth.json")
	defer func() { _ = writeJSON(truthPath, truth) }()
	if err := sc.Ready(ctx, rn.lab, truth); err != nil {
		return fail("readiness", err)
	}
	// Written before Yuanfang starts, so a crash still leaves the answer.
	if err := writeJSON(truthPath, truth); err != nil {
		return fail("setup", err)
	}

	casesDir := filepath.Join(dir, "cases")
	agentErr := rn.investigate(ctx, sc.Question(), dir, casesDir)
	if ctx.Err() != nil {
		return fail("interrupted", ctx.Err())
	}
	if err := sc.Recheck(ctx, rn.lab, truth); err != nil {
		return fail("recheck", err)
	}

	c, err := loadCase(casesDir)
	if err != nil {
		// No case means Yuanfang never started an investigation (config,
		// credentials, ...): nothing to grade.
		if agentErr != nil {
			err = fmt.Errorf("%w (yuanfang: %w; see %s)", err, agentErr, filepath.Join(dir, "yuanfang.log"))
		}
		return fail("load case", err)
	}
	m := c.Meta
	r.CaseID, r.Model, r.Steps, r.Tokens = m.ID, m.Provider+"/"+m.Model, m.Steps, m.Tokens
	if !m.Finished.IsZero() {
		r.Duration = m.Finished.Sub(m.Started)
	}
	if c.Report == nil {
		// The investigation started but produced no report: Yuanfang's failure.
		r.Outcome = Fail
		r.Error = "no report: " + firstNonEmpty(m.Error, errString(agentErr), "case "+m.Status)
		return r
	}
	r.Checks = append(sharedChecks(c), sc.Grade(truth, c)...)
	r.Outcome = outcome(r.Checks)
	return r
}

// investigate runs `yuanfang ask` against the lab as a separate process,
// with a config written for this run. Yuanfang gets the complaint and the
// least-privilege lab DSN, nothing else.
func (rn *runner) investigate(ctx context.Context, question, dir, casesDir string) error {
	cfg := filepath.Join(dir, "yuanfang.yaml")
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf(`# Written by the eval for one run. Production defaults except the lab target.
targets:
  - name: lab
    type: mysql
    description: Yuanfang lab MySQL 8.4 with the seeded shop schema
    dsn_env: YUANFANG_LAB_DSN
safety:
  redact: true
  query_timeout: 10s
  allow_explain: false
cases_dir: %q
`, mustAbs(casesDir))), 0o600); err != nil {
		return err
	}
	log, err := os.Create(filepath.Join(dir, "yuanfang.log")) //nolint:gosec // run directory created by the eval
	if err != nil {
		return err
	}
	defer log.Close()

	args := []string{"--config", cfg, "ask", question, "-t", "lab", "--json"}
	if rn.model != "" {
		args = append(args, "-m", rn.model)
	}
	tctx, cancel := context.WithTimeout(ctx, rn.timeout)
	defer cancel()
	cmd := exec.CommandContext(tctx, rn.bin, args...) //nolint:gosec // binary built by the eval
	cmd.Env = append(agentEnv(), "YUANFANG_LAB_DSN="+rn.lab.YuanfangDSN())
	cmd.Stdout, cmd.Stderr = log, log
	cmd.WaitDelay = 5 * time.Second
	err = cmd.Run()
	if errors.Is(tctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("timed out after %s", rn.timeout)
	}
	return err
}

// agentEnv is the eval's environment minus anything that could point
// Yuanfang at another database or another config.
func agentEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "YUANFANG_CONFIG" || (strings.HasPrefix(k, "YUANFANG_") && strings.HasSuffix(k, "_DSN")) {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
