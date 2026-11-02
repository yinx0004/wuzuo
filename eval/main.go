// SPDX-License-Identifier: Apache-2.0

// Command eval examines Yuanfang on incidents staged in the local lab and
// grades each diagnosis against the ground truth. Lab only: it refuses to run
// unless the lab container answers on 127.0.0.1:13306.
//
//	make lab-up
//	go run ./eval -scenario lock-wait -runs 3 -m sonnet
//
// Each run restarts the lab MySQL, causes the incident, waits until it is
// observable, runs `yuanfang ask` as a separate process and grades the case
// it writes. Results go to eval/results/<time>/ (gitignored).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "eval:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		scenario = flag.String("scenario", "lock-wait", "scenario to run: "+strings.Join(scenarioNames(), ", "))
		runs     = flag.Int("runs", 1, "how many times to run it")
		model    = flag.String("m", "", "model for yuanfang ask -m (default: Yuanfang's default choice)")
		timeout  = flag.Duration("timeout", 5*time.Minute, "time limit for one investigation")
		out      = flag.String("out", "eval/results", "results directory")
	)
	flag.Parse()
	if scenarios[*scenario] == nil {
		return fmt.Errorf("unknown scenario %q (have %s)", *scenario, strings.Join(scenarioNames(), ", "))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	lab, err := OpenLab(ctx)
	if err != nil {
		return err
	}
	defer lab.Close()

	dir := filepath.Join(*out, time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	bin := filepath.Join(dir, "yuanfang")
	commit := gitDescribe()
	fmt.Fprintf(os.Stderr, "building yuanfang %s\n", commit)
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", bin, //nolint:gosec // fixed arguments
		"-ldflags", "-X github.com/yinx0004/yuanfang/internal/version.Version="+commit, "./cmd/yuanfang")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("build: %w", err)
	}

	rn := &runner{lab: lab, bin: mustAbs(bin), model: *model, timeout: *timeout}
	var results []Run
	for i := 1; i <= *runs && ctx.Err() == nil; i++ {
		fmt.Fprintf(os.Stderr, "%s run %d/%d ...\n", *scenario, i, *runs)
		r := rn.run(ctx, *scenario, i, filepath.Join(dir, fmt.Sprintf("%s-%d", *scenario, i)))
		fmt.Fprintf(os.Stderr, "  %s %s\n", r.Outcome, r.Error)
		results = append(results, r)
	}

	summary := scorecard(commit, results)
	if err := os.WriteFile(filepath.Join(dir, "scorecard.md"), []byte(summary), 0o600); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "results.json"), map[string]any{
		"version": commit, "scenario": *scenario, "model_flag": *model, "runs": results,
	}); err != nil {
		return err
	}
	fmt.Println(summary)
	fmt.Fprintf(os.Stderr, "results: %s/\n", dir)
	return nil
}

func scenarioNames() []string {
	var n []string
	for k := range scenarios {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

func gitDescribe() string {
	out, err := exec.Command("git", "describe", "--tags", "--always", "--dirty").Output()
	if err != nil {
		return "dev"
	}
	return strings.TrimSpace(string(out))
}

// scorecard renders the runs as one table: a column per check, kept apart
// rather than summed into a score.
func scorecard(version string, runs []Run) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Eval scorecard\n\nYuanfang `%s`\n\n", version)
	var names []string
	for _, r := range runs {
		if len(r.Checks) > 0 {
			for _, c := range r.Checks {
				names = append(names, c.Name)
			}
			break
		}
	}
	b.WriteString("| run | outcome |")
	for _, n := range names {
		b.WriteString(" " + n + " |")
	}
	b.WriteString(" steps | tokens in/out | time | model |\n|---|---|")
	b.WriteString(strings.Repeat("---|", len(names)+4) + "\n")
	pass, errs := 0, 0
	for _, r := range runs {
		switch r.Outcome {
		case Pass:
			pass++
		case Error:
			errs++
		}
		fmt.Fprintf(&b, "| %s-%d | %s |", r.Scenario, r.Index, r.Outcome)
		for i := range names {
			mark := ""
			if i < len(r.Checks) {
				mark = "✗"
				if r.Checks[i].Pass {
					mark = "✓"
				}
			}
			b.WriteString(" " + mark + " |")
		}
		fmt.Fprintf(&b, " %d | %d/%d | %s | %s |\n", r.Steps, r.Tokens.TotalInput(), r.Tokens.Output, r.Duration.Round(time.Second), r.Model)
	}
	// ERROR runs say nothing about Yuanfang and are left out of the rate.
	fmt.Fprintf(&b, "\n%d/%d graded runs passed, %d environment errors\n", pass, len(runs)-errs, errs)
	for _, r := range runs {
		if r.Error != "" {
			fmt.Fprintf(&b, "\n- %s-%d %s: %s", r.Scenario, r.Index, r.Outcome, r.Error)
		}
		for _, c := range r.Checks {
			if !c.Pass {
				fmt.Fprintf(&b, "\n- %s-%d %s/%s: %s", r.Scenario, r.Index, c.Dimension, c.Name, c.Reason)
			}
		}
	}
	b.WriteString("\n")
	return b.String()
}
