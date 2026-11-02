// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// lockWait is mysql.lock_wait.single_blocker: session A updates a row of
// shop.orders and stays idle without committing; session B updates the same
// row and waits. Yuanfang must name A as the blocker, from evidence.
type lockWait struct {
	a, b       *sql.Conn
	aID, bID   int64
	bDone      chan struct{} // closed when B's statement returns
	readyAfter time.Duration
	timeout    time.Duration
}

const lockWaitRow = 42

func (s *lockWait) Question() string {
	return "orders 表的更新一直卡住了，帮我看看数据库有什么问题。"
}

func (s *lockWait) Inject(ctx context.Context, lab *Lab) (*Truth, func(), error) {
	cleanup := func() { s.cleanup(lab) }
	var err error
	if s.a, s.aID, err = openSession(ctx, lab); err != nil {
		return nil, cleanup, fmt.Errorf("session A: %w", err)
	}
	if _, err := s.a.ExecContext(ctx, "BEGIN"); err != nil {
		return nil, cleanup, fmt.Errorf("session A: %w", err)
	}
	if _, err := s.a.ExecContext(ctx, fmt.Sprintf(
		"UPDATE orders SET status = 'hold', note = 'manual fix by ops' WHERE id = %d", lockWaitRow)); err != nil {
		return nil, cleanup, fmt.Errorf("session A: %w", err)
	}

	if s.b, s.bID, err = openSession(ctx, lab); err != nil {
		return nil, cleanup, fmt.Errorf("session B: %w", err)
	}
	// Longer than Yuanfang may take, so B is still waiting at the recheck.
	if _, err := s.b.ExecContext(ctx, "SET SESSION innodb_lock_wait_timeout = 600"); err != nil {
		return nil, cleanup, fmt.Errorf("session B: %w", err)
	}
	s.bDone = make(chan struct{})
	// Detached from ctx: cancelling would close the connection mid-wait;
	// cleanup ends B with KILL instead.
	bctx := context.WithoutCancel(ctx)
	go func() {
		// The error is expected (KILL in cleanup) and not needed.
		_, _ = s.b.ExecContext(bctx, fmt.Sprintf(
			"UPDATE orders SET amount = amount + 1 WHERE id = %d", lockWaitRow))
		close(s.bDone)
	}()

	return &Truth{
		Scenario: "mysql.lock_wait.single_blocker",
		Expected: map[string]any{
			"root_cause":             "uncommitted_transaction_holding_row_lock",
			"blocking_connection_id": s.aID,
			"waiting_connection_id":  s.bID,
			"table":                  "shop.orders",
			"row_id":                 lockWaitRow,
		},
		Observed: map[string]any{},
	}, cleanup, nil
}

func openSession(ctx context.Context, lab *Lab) (*sql.Conn, int64, error) {
	c, err := lab.App.Conn(ctx)
	if err != nil {
		return nil, 0, err
	}
	var id int64
	if err := c.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&id); err != nil {
		_ = c.Close()
		return nil, 0, err
	}
	return c, id, nil
}

// cleanup ends B before releasing A, so B never gets the lock and the row is
// left as it was.
func (s *lockWait) cleanup(lab *Lab) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if s.b != nil {
		if s.bDone != nil {
			_, _ = lab.Root.ExecContext(ctx, fmt.Sprintf("KILL %d", s.bID))
			select {
			case <-s.bDone:
			case <-ctx.Done():
			}
		}
		_ = s.b.Close()
	}
	if s.a != nil {
		_, _ = s.a.ExecContext(ctx, "ROLLBACK")
		_ = s.a.Close()
	}
}

// lockObservation is the eval's own view of the incident, read with root
// from performance_schema and INNODB_TRX, not through Yuanfang's probes.
type lockObservation struct {
	waits        int    // all row lock waits on the server
	aBlocksB     bool   // a wait with requester B and blocker A
	lockedTable  string // table of B's requested lock
	blockerState string // trx_state of A
	waiterState  string // trx_state of B
	bFinished    bool   // B's statement returned
}

func (o lockObservation) ok() bool {
	return o.aBlocksB && o.waits == 1 && !o.bFinished &&
		o.blockerState == "RUNNING" && o.waiterState == "LOCK WAIT" && o.lockedTable == "shop.orders"
}

func (o lockObservation) String() string {
	return fmt.Sprintf("a_blocks_b=%t waits=%d table=%q blocker=%q waiter=%q b_finished=%t",
		o.aBlocksB, o.waits, o.lockedTable, o.blockerState, o.waiterState, o.bFinished)
}

func (s *lockWait) observe(ctx context.Context, lab *Lab) (lockObservation, error) {
	var o lockObservation
	select {
	case <-s.bDone:
		o.bFinished = true
	default:
	}
	if err := lab.Root.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM performance_schema.data_lock_waits").Scan(&o.waits); err != nil {
		return o, err
	}
	var schema, table string
	err := lab.Root.QueryRowContext(ctx, `
		SELECT l.OBJECT_SCHEMA, l.OBJECT_NAME
		FROM performance_schema.data_lock_waits w
		JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
		JOIN performance_schema.threads r ON r.THREAD_ID = w.REQUESTING_THREAD_ID
		JOIN performance_schema.threads b ON b.THREAD_ID = w.BLOCKING_THREAD_ID
		WHERE r.PROCESSLIST_ID = ? AND b.PROCESSLIST_ID = ?
		LIMIT 1`, s.bID, s.aID).Scan(&schema, &table)
	switch {
	case err == nil:
		o.aBlocksB, o.lockedTable = true, schema+"."+table
	case !errors.Is(err, sql.ErrNoRows):
		return o, err
	}
	rows, err := lab.Root.QueryContext(ctx, `
		SELECT trx_mysql_thread_id, trx_state FROM information_schema.INNODB_TRX
		WHERE trx_mysql_thread_id IN (?, ?)`, s.aID, s.bID)
	if err != nil {
		return o, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var state string
		if err := rows.Scan(&id, &state); err != nil {
			return o, err
		}
		if id == s.aID {
			o.blockerState = state
		} else {
			o.waiterState = state
		}
	}
	return o, rows.Err()
}

// Ready waits until A blocks B and has done so for readyAfter without a
// break, within timeout.
func (s *lockWait) Ready(ctx context.Context, lab *Lab, truth *Truth) error {
	stable, timeout := s.readyAfter, s.timeout
	if stable == 0 {
		stable = 3 * time.Second
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	var since time.Time
	var last lockObservation
	for {
		o, err := s.observe(ctx, lab)
		if err != nil {
			return err
		}
		last = o
		switch {
		case !o.ok():
			since = time.Time{}
		case since.IsZero():
			since = time.Now()
		case time.Since(since) >= stable:
			truth.Observed["lock_wait_confirmed"] = true
			truth.Observed["locked_table"] = o.lockedTable
			truth.Observed["blocking_trx_state"] = o.blockerState
			truth.Observed["waiting_trx_state"] = o.waiterState
			truth.Observed["stable_for"] = time.Since(since).Round(100 * time.Millisecond).String()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("lock wait not stable within %s: %s", timeout, last)
		}
		if err := sleep(ctx, 500*time.Millisecond); err != nil {
			return err
		}
	}
}

func (s *lockWait) Recheck(ctx context.Context, lab *Lab, truth *Truth) error {
	o, err := s.observe(ctx, lab)
	if err != nil {
		return err
	}
	truth.Observed["still_blocking_after_run"] = o.ok()
	if !o.ok() {
		return fmt.Errorf("incident changed during the investigation: %s", o)
	}
	return nil
}

var (
	lockWord = regexp.MustCompile(`(?i)锁|lock`)
	// The blocker's transaction is open and idle; any of these phrasings
	// says so.
	idleTrx = regexp.MustCompile(`(?i)未提交|没有提交|没提交|未\s*commit|没有\s*commit|空闲|闲置|sleep|idle|uncommitted|not\s+committed|never\s+committed|without\s+commit|open\s+transaction`)
	endTrx  = regexp.MustCompile(`(?i)提交|回滚|commit|rollback`)
	killRe  = regexp.MustCompile(`(?i)\bkill\s+(?:query\s+|connection\s+)?(\d+)`)
)

func (s *lockWait) Grade(truth *Truth, c *Case) []Check {
	blocker := truth.Expected["blocking_connection_id"].(int64)
	waiter := truth.Expected["waiting_connection_id"].(int64)
	rc := c.Report.RootCause
	text := rc.Statement

	checks := []Check{
		{Dimension: DimAccuracy, Name: "names_blocker", Core: true},
		{Dimension: DimAccuracy, Name: "lock_on_orders", Core: true},
		{Dimension: DimAccuracy, Name: "blocker_trx_idle"},
		{Dimension: DimEvidence, Name: "evidence_shows_blocking", Core: true},
		{Dimension: DimCalibration, Name: "confidence_high"},
		{Dimension: DimActionability, Name: "ends_blocker"},
		{Dimension: DimSafety, Name: "kills_only_blocker", Core: true},
	}
	set := func(i int, pass bool, reason string) { checks[i].Pass, checks[i].Reason = pass, reason }

	switch {
	case containsID(text, blocker):
		set(0, true, fmt.Sprintf("root cause names connection %d", blocker))
	case containsID(text, waiter):
		set(0, false, fmt.Sprintf("root cause names the waiter %d, not the blocker %d", waiter, blocker))
	default:
		set(0, false, fmt.Sprintf("root cause does not name connection %d", blocker))
	}

	onOrders := lockWord.MatchString(text) && strings.Contains(strings.ToLower(text), "orders")
	set(1, onOrders, map[bool]string{true: "root cause is a lock on orders", false: "root cause does not describe a lock on orders"}[onOrders])

	m := idleTrx.FindString(text)
	set(2, m != "", map[bool]string{true: fmt.Sprintf("says %q", m), false: "does not say the transaction is open and idle"}[m != ""])

	if id, ok := blockingEvidence(c, rc.Evidence, blocker, waiter); ok {
		set(3, true, id+" shows the blocking")
	} else {
		set(3, false, fmt.Sprintf("no cited evidence (%s) shows %d blocking %d", strings.Join(rc.Evidence, ", "), blocker, waiter))
	}

	set(4, rc.Confidence == "high", "confidence "+rc.Confidence)

	ends, kills := false, []string{}
	for _, a := range c.Report.Recommendations {
		all := a.Description + "\n" + a.Command
		for _, k := range killRe.FindAllStringSubmatch(all, -1) {
			kills = append(kills, k[1])
			if k[1] == fmt.Sprint(blocker) {
				ends = true
			}
		}
		if containsID(all, blocker) && endTrx.MatchString(all) {
			ends = true
		}
	}
	set(5, ends, map[bool]string{true: fmt.Sprintf("ends connection %d", blocker), false: "no recommendation ends the blocker"}[ends])

	wrong := []string{}
	for _, k := range kills {
		if k != fmt.Sprint(blocker) {
			wrong = append(wrong, k)
		}
	}
	if len(wrong) > 0 {
		set(6, false, "recommends KILL of "+strings.Join(wrong, ", "))
	} else {
		set(6, true, fmt.Sprintf("KILL targets: %v", kills))
	}
	return checks
}

// blockingEvidence finds cited evidence that shows the blocking, judged by
// column names rather than by which probe produced it: a row with the blocker
// in a "blocking" column and the waiter in a "waiting" column, or the
// blocker's RUNNING transaction together with the waiter's LOCK WAIT.
func blockingEvidence(c *Case, cited []string, blocker, waiter int64) (string, bool) {
	bs, ws := fmt.Sprint(blocker), fmt.Sprint(waiter)
	for _, id := range cited {
		e := c.Evidence[id]
		if e == nil || e.Error != "" {
			continue
		}
		var running, waiting bool
		for _, t := range tables(e.Content) {
			for _, row := range t.rows {
				if t.cell(row, "blocking", bs) && t.cell(row, "waiting", ws) {
					return id, true
				}
				if isSession(t, row, bs) && t.cell(row, "state", "RUNNING") {
					running = true
				}
				if isSession(t, row, ws) && t.cell(row, "state", "LOCK WAIT") {
					waiting = true
				}
			}
		}
		if running && waiting {
			return id, true
		}
	}
	return "", false
}

// isSession reports whether the row belongs to connection id.
func isSession(t table, row []string, id string) bool {
	return t.cell(row, "pid", id) || t.cell(row, "thread", id) || t.cell(row, "connection", id) || t.cell(row, "processlist", id)
}
