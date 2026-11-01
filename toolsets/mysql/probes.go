// SPDX-License-Identifier: Apache-2.0

package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yinx0004/yuanfang/pkg/toolset"
)

func intProp(desc string, min, max int) map[string]any {
	return map[string]any{"type": "integer", "minimum": min, "maximum": max, "description": desc}
}

// clampInt applies a default for zero and clamps to [1, max].
func clampInt(v, def, max int) int {
	if v == 0 {
		v = def
	}
	if v < 1 {
		return 1
	}
	if v > max {
		return max
	}
	return v
}

// Tools implements toolset.Toolset.
func (ts *Toolset) Tools() []toolset.Tool {
	probes := []*probe{
		{
			name: "mysql_overview",
			desc: "Server identity and health at a glance: version, uptime, role (read_only), key configuration (max_connections, buffer pool, durability settings) and current connection/thread counts. Good first probe.",
			run:  ts.overview,
		},
		{
			name: "mysql_processlist",
			desc: "Current sessions from performance_schema.processlist, longest-running first, plus a summary grouped by user/command/state. Use to spot pile-ups, long queries and connection storms.",
			props: map[string]any{
				"include_sleep": map[string]any{"type": "boolean", "description": "Include idle (Sleep) sessions. Default false."},
				"min_seconds":   intProp("Only sessions in their current state for at least this many seconds.", 0, 86400),
				"limit":         intProp("Max sessions to list (default 50).", 1, 500),
			},
			run: ts.processlist,
		},
		{
			name: "mysql_transactions",
			desc: "Open InnoDB transactions (information_schema.INNODB_TRX), oldest first, with rows locked/modified, the owning session and the last statement it ran. Finds long or idle-in-transaction sessions holding locks or blocking purge.",
			props: map[string]any{
				"limit": intProp("Max transactions (default 30).", 1, 200),
			},
			run: ts.transactions,
		},
		{
			name: "mysql_lock_waits",
			desc: "Who is blocking whom right now: InnoDB row lock waits (sys.innodb_lock_waits) and metadata lock waits (sys.schema_table_lock_waits), with waiting and blocking sessions and queries. A NULL blocking_query means the blocker is idle inside an open transaction.",
			props: map[string]any{
				"limit": intProp("Max waits per section (default 30).", 1, 200),
			},
			run: ts.lockWaits,
		},
		{
			name: "mysql_innodb_status",
			desc: "Selected sections of SHOW ENGINE INNODB STATUS: LATEST DETECTED DEADLOCK, TRANSACTIONS, SEMAPHORES, BUFFER POOL AND MEMORY, ROW OPERATIONS, LOG, FILE I/O. Use for deadlocks, purge lag (history list length) and internal contention.",
			props: map[string]any{
				"sections": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string", "enum": innodbSections},
					"description": "Sections to return (default: all listed).",
				},
			},
			run: ts.innodbStatus,
		},
		{
			name: "mysql_top_statements",
			desc: "Top statement digests from performance_schema.events_statements_summary_by_digest. Counters are cumulative since server start or the last reset; use last_seen_minutes to focus on recent activity. Finds expensive, frequent, or index-less queries.",
			props: map[string]any{
				"order_by":          map[string]any{"type": "string", "enum": statementOrders, "description": "Default total_latency."},
				"schema":            map[string]any{"type": "string", "description": "Only this schema."},
				"last_seen_minutes": intProp("Only digests executed within the last N minutes.", 1, 10080),
				"limit":             intProp("Max digests (default 15).", 1, 100),
			},
			run: ts.topStatements,
		},
		{
			name:    "mysql_status_delta",
			desc:    "Samples SHOW GLOBAL STATUS twice and reports per-second rates (QPS, row operations, row lock waits, buffer pool misses, temp disk tables, aborted connections...) plus current gauges such as Threads_running and the InnoDB history list length. Use to see what the server is doing right now.",
			timeout: 30 * time.Second,
			props: map[string]any{
				"interval_seconds": intProp("Sampling window (default 5).", 1, 30),
			},
			run: ts.statusDelta,
		},
		{
			name: "mysql_replication",
			desc: "Replication health: replica status per channel (IO/SQL threads, Seconds_Behind_Source, last errors, GTID sets), applier worker errors, and source-side binlog position and connected replicas.",
			run:  ts.replication,
		},
		{
			name: "mysql_error_log",
			desc: "Recent server error log entries from performance_schema.error_log (MySQL 8.0.22+).",
			props: map[string]any{
				"since_minutes": intProp("Look back this many minutes (default 60).", 1, 10080),
				"min_priority":  map[string]any{"type": "string", "enum": []string{"Error", "Warning", "Note"}, "description": "Minimum severity (default Warning)."},
				"limit":         intProp("Max entries (default 50).", 1, 500),
			},
			run: ts.errorLog,
		},
		{
			name: "mysql_variables",
			desc: "Read global system variables by exact name or LIKE pattern, e.g. 'innodb_%timeout%'.",
			props: map[string]any{
				"names":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Exact variable names."},
				"pattern": map[string]any{"type": "string", "description": "LIKE pattern."},
			},
			run: ts.variables,
		},
		{
			name: "mysql_table_stats",
			desc: "Table size and shape from information_schema (estimates, cached per information_schema_stats_expiry): largest tables in a schema, or for one table its columns, indexes with cardinality, and per-index read/write counts since startup (shows unused indexes and full scans). Requires some privilege on the schema.",
			props: map[string]any{
				"schema": map[string]any{"type": "string"},
				"table":  map[string]any{"type": "string", "description": "Optional; omit to list the largest tables."},
			},
			req: []string{"schema"},
			run: ts.tableStats,
		},
	}
	// mysql_explain is off by default (safety.allow_explain) because the model
	// writes the statement it explains; see (*Toolset).explain for the design,
	// its limits, and the digest-based redesign that would let it default on.
	if ts.opts.AllowExplain {
		probes = append(probes, &probe{
			name: "mysql_explain",
			desc: "EXPLAIN (never EXPLAIN ANALYZE) a SELECT to see its plan. Digest text uses '?' placeholders: substitute representative literal values first. " +
				"Write statements cannot be explained by this read-only account: to see how an UPDATE or DELETE locates its rows, explain a SELECT with the same FROM/JOIN/WHERE/ORDER BY/LIMIT instead. " +
				"Under REPEATABLE READ an UPDATE/DELETE locks every row it scans, so a table scan here means the write locks the whole table's rows. " +
				"Requires SELECT privilege on the tables involved.",
			props: map[string]any{
				"schema":    map[string]any{"type": "string", "description": "Default schema for unqualified table names."},
				"statement": map[string]any{"type": "string", "description": "A single SELECT/WITH statement."},
			},
			req: []string{"schema", "statement"},
			run: ts.explain,
		})
	}
	// mysql_query is an escape hatch, off by default (safety.allow_raw_sql).
	//
	// The dedicated probes cover the common incident shapes. Some
	// investigations need a diagnostic view none of them reads, for example:
	//   - memory by instrument: performance_schema.memory_summary_global_by_event_name
	//   - IO hot spots: sys.io_global_by_file_by_latency, sys.io_global_by_wait_by_latency
	//   - per user/host load: sys.user_summary, sys.host_summary
	//   - auto-increment exhaustion: sys.schema_auto_increment_columns
	//   - redundant or unused indexes: sys.schema_redundant_indexes, sys.schema_unused_indexes
	//   - what an idle-in-transaction session ran earlier:
	//     performance_schema.events_statements_history for its THREAD_ID
	//   - raw lock detail: performance_schema.data_locks
	//
	// Because the model writes the statement, it carries the same risk as
	// mysql_explain and the same layers apply (GuardReadOnly, guardedRun,
	// row cap). Its description asks the model to stay on diagnostic schemas,
	// but only privileges actually enforce that: grants on application schemas
	// make their rows readable here. Queries that investigations keep issuing
	// through mysql_query are candidates for new dedicated probes.
	if ts.opts.AllowRawSQL {
		probes = append(probes, &probe{
			name: "mysql_query",
			desc: "Run one guarded read-only diagnostic query (SELECT/SHOW/EXPLAIN/DESCRIBE) when no dedicated probe fits, typically against performance_schema, sys or information_schema. Results are capped at 200 rows. Do not read application data.",
			props: map[string]any{
				"statement": map[string]any{"type": "string"},
				"schema":    map[string]any{"type": "string", "description": "Optional default schema."},
			},
			req: []string{"statement"},
			run: ts.rawQuery,
		})
	}
	tools := make([]toolset.Tool, len(probes))
	for i, p := range probes {
		p.ts = ts
		tools[i] = p
	}
	return tools
}

func (ts *Toolset) overview(ctx context.Context, db *sql.DB, _ json.RawMessage) (string, error) {
	var b strings.Builder
	var version, comment, host string
	if err := db.QueryRowContext(ctx, "SELECT VERSION(), @@version_comment, @@hostname").Scan(&version, &comment, &host); err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "server: %s (%s) on %s\n\n", version, comment, host)

	_, vars, err := query(ctx, db, `SHOW GLOBAL VARIABLES WHERE Variable_name IN (
		'read_only','super_read_only','max_connections','max_user_connections','innodb_buffer_pool_size',
		'innodb_flush_log_at_trx_commit','sync_binlog','transaction_isolation','innodb_lock_wait_timeout',
		'lock_wait_timeout','long_query_time','slow_query_log','max_execution_time','innodb_io_capacity',
		'innodb_redo_log_capacity','thread_cache_size','table_open_cache','tmp_table_size','binlog_format',
		'gtid_mode','innodb_deadlock_detect','performance_schema','wait_timeout')`)
	if err != nil {
		return "", err
	}
	b.WriteString("## configuration\n")
	b.WriteString(toolset.Table([]string{"variable", "value"}, vars))

	_, status, err := query(ctx, db, `SHOW GLOBAL STATUS WHERE Variable_name IN (
		'Uptime','Threads_connected','Threads_running','Threads_created','Max_used_connections',
		'Max_used_connections_time','Aborted_connects','Connection_errors_max_connections',
		'Innodb_row_lock_current_waits','Innodb_buffer_pool_pages_total','Innodb_buffer_pool_pages_free',
		'Innodb_buffer_pool_pages_dirty','Slow_queries','Questions')`)
	if err != nil {
		return b.String(), err
	}
	b.WriteString("\n## status (cumulative counters are since startup)\n")
	b.WriteString(toolset.Table([]string{"status", "value"}, status))
	return b.String(), nil
}

var processlistSources = []struct{ name, list, summary string }{
	{
		name: "performance_schema.processlist",
		list: `SELECT ID, USER, HOST, DB, COMMAND, TIME, STATE, INFO FROM performance_schema.processlist
			WHERE ID <> CONNECTION_ID() AND (COMMAND <> 'Sleep' OR ?) AND TIME >= ? AND COMMAND <> 'Daemon'
			ORDER BY TIME DESC LIMIT ?`,
		summary: `SELECT USER, COMMAND, IFNULL(STATE, ''), COUNT(*) AS sessions, MAX(TIME) AS max_time_s
			FROM performance_schema.processlist WHERE ID <> CONNECTION_ID()
			GROUP BY USER, COMMAND, STATE ORDER BY sessions DESC LIMIT 20`,
	},
	{
		name: "information_schema.PROCESSLIST",
		list: `SELECT ID, USER, HOST, DB, COMMAND, TIME, STATE, INFO FROM information_schema.PROCESSLIST
			WHERE ID <> CONNECTION_ID() AND (COMMAND <> 'Sleep' OR ?) AND TIME >= ? AND COMMAND <> 'Daemon'
			ORDER BY TIME DESC LIMIT ?`,
		summary: `SELECT USER, COMMAND, IFNULL(STATE, ''), COUNT(*) AS sessions, MAX(TIME) AS max_time_s
			FROM information_schema.PROCESSLIST WHERE ID <> CONNECTION_ID()
			GROUP BY USER, COMMAND, STATE ORDER BY sessions DESC LIMIT 20`,
	},
}

func (ts *Toolset) processlist(ctx context.Context, db *sql.DB, raw json.RawMessage) (string, error) {
	var a struct {
		IncludeSleep bool `json:"include_sleep"`
		MinSeconds   int  `json:"min_seconds"`
		Limit        int  `json:"limit"`
	}
	if err := toolset.DecodeArgs(raw, &a); err != nil {
		return "", err
	}
	limit := clampInt(a.Limit, 50, 500)
	// Older 8.0 releases lack performance_schema.processlist; fall back to
	// information_schema. Both queries are constants.
	src := processlistSources[0]
	header, rows, err := query(ctx, db, src.list, a.IncludeSleep, a.MinSeconds, limit)
	if err != nil {
		src = processlistSources[1]
		header, rows, err = query(ctx, db, src.list, a.IncludeSleep, a.MinSeconds, limit)
		if err != nil {
			return "", err
		}
	}
	for _, r := range rows {
		r[7] = ts.sqlText(r[7], 300)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## sessions (source: %s, include_sleep=%v, min_seconds=%d)\n", src.name, a.IncludeSleep, a.MinSeconds)
	b.WriteString(toolset.Table(lower(header), rows))

	_, summary, err := query(ctx, db, src.summary)
	if err == nil {
		b.WriteString("\n## summary of all sessions\n")
		b.WriteString(toolset.Table([]string{"user", "command", "state", "sessions", "max_time_s"}, summary))
	}
	return b.String(), nil
}

func (ts *Toolset) transactions(ctx context.Context, db *sql.DB, raw json.RawMessage) (string, error) {
	var a struct {
		Limit int `json:"limit"`
	}
	if err := toolset.DecodeArgs(raw, &a); err != nil {
		return "", err
	}
	header, rows, err := query(ctx, db, `
		SELECT t.trx_id, t.trx_state, t.trx_started,
		       TIMESTAMPDIFF(SECOND, t.trx_started, NOW()) AS age_s,
		       t.trx_mysql_thread_id AS pid, th.PROCESSLIST_USER AS user, th.PROCESSLIST_HOST AS host,
		       th.PROCESSLIST_COMMAND AS command, th.PROCESSLIST_TIME AS command_time_s,
		       t.trx_rows_locked, t.trx_rows_modified, t.trx_tables_locked,
		       t.trx_isolation_level, t.trx_operation_state, t.trx_wait_started,
		       t.trx_query AS current_query, esc.SQL_TEXT AS last_statement
		FROM information_schema.INNODB_TRX t
		LEFT JOIN performance_schema.threads th ON th.PROCESSLIST_ID = t.trx_mysql_thread_id
		LEFT JOIN performance_schema.events_statements_current esc ON esc.THREAD_ID = th.THREAD_ID
		ORDER BY t.trx_started LIMIT ?`, clampInt(a.Limit, 30, 200))
	if err != nil {
		return "", err
	}
	for _, r := range rows {
		r[15] = ts.sqlText(r[15], 300)
		r[16] = ts.sqlText(r[16], 300)
	}
	return "## open InnoDB transactions (oldest first)\n" + toolset.Table(header, rows), nil
}

func (ts *Toolset) lockWaits(ctx context.Context, db *sql.DB, raw json.RawMessage) (string, error) {
	var a struct {
		Limit int `json:"limit"`
	}
	if err := toolset.DecodeArgs(raw, &a); err != nil {
		return "", err
	}
	limit := clampInt(a.Limit, 30, 200)
	var b strings.Builder
	header, rows, err := query(ctx, db, `
		SELECT wait_started, wait_age_secs, locked_table, locked_index, locked_type,
		       waiting_pid, waiting_lock_mode, waiting_trx_age, waiting_query,
		       blocking_pid, blocking_lock_mode, blocking_trx_age, blocking_trx_rows_locked, blocking_query
		FROM sys.innodb_lock_waits ORDER BY wait_age_secs DESC LIMIT ?`, limit)
	if err != nil {
		return "", fmt.Errorf("sys.innodb_lock_waits: %w", err)
	}
	for _, r := range rows {
		r[8] = ts.sqlText(r[8], 250)
		r[13] = ts.sqlText(r[13], 250)
	}
	b.WriteString("## InnoDB row lock waits\n")
	b.WriteString(toolset.Table(header, rows))

	if len(rows) > 0 {
		_, blockers, err := query(ctx, db, `
			SELECT w.blocking_pid, COUNT(DISTINCT w.waiting_pid) AS waiters, MAX(w.wait_age_secs) AS longest_wait_s,
			       IF(w.blocking_pid IN (SELECT waiting_pid FROM sys.innodb_lock_waits), 'no', 'yes') AS root_blocker
			FROM sys.innodb_lock_waits w GROUP BY w.blocking_pid
			ORDER BY root_blocker DESC, waiters DESC LIMIT 10`)
		if err == nil {
			b.WriteString("\n## blockers (root_blocker=yes: not itself waiting, i.e. the head of the chain)\n")
			b.WriteString(toolset.Table([]string{"blocking_pid", "waiters", "longest_wait_s", "root_blocker"}, blockers))
		}
	}

	header, rows, err = query(ctx, db, `
		SELECT object_schema, object_name, waiting_pid, waiting_account, waiting_lock_type,
		       waiting_query_secs, waiting_query, blocking_pid, blocking_account,
		       blocking_lock_type, blocking_lock_duration
		FROM sys.schema_table_lock_waits ORDER BY waiting_query_secs DESC LIMIT ?`, limit)
	b.WriteString("\n## metadata lock waits\n")
	if err != nil {
		fmt.Fprintf(&b, "unavailable: %v\n", err)
	} else {
		for _, r := range rows {
			r[6] = ts.sqlText(r[6], 250)
		}
		b.WriteString(toolset.Table(header, rows))
	}
	return b.String(), nil
}

var innodbSections = []string{
	"LATEST DETECTED DEADLOCK", "TRANSACTIONS", "SEMAPHORES", "BUFFER POOL AND MEMORY",
	"ROW OPERATIONS", "LOG", "FILE I/O",
}

var (
	sectionHeader = regexp.MustCompile(`(?m)^-{3,}\n([A-Z][A-Z /]+[A-Z])\n-{3,}$`)
	recordDump    = regexp.MustCompile(`(?m)^(\s*\d+: len \d+; hex )[0-9a-f]+; asc .*$`)
	sqlLine       = regexp.MustCompile(`(?mi)^(SELECT|INSERT|UPDATE|DELETE|REPLACE|WITH|CALL)\b.*$`)
)

// splitInnoDBStatus maps section titles to their bodies.
func splitInnoDBStatus(s string) map[string]string {
	out := map[string]string{}
	idx := sectionHeader.FindAllStringSubmatchIndex(s, -1)
	for i, m := range idx {
		title := s[m[2]:m[3]]
		end := len(s)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		out[title] = strings.TrimSpace(s[m[1]:end])
	}
	return out
}

func (ts *Toolset) innodbStatus(ctx context.Context, db *sql.DB, raw json.RawMessage) (string, error) {
	var a struct {
		Sections []string `json:"sections"`
	}
	if err := toolset.DecodeArgs(raw, &a); err != nil {
		return "", err
	}
	if len(a.Sections) == 0 {
		a.Sections = innodbSections
	}
	var typ, name, status string
	if err := db.QueryRowContext(ctx, "SHOW ENGINE INNODB STATUS").Scan(&typ, &name, &status); err != nil {
		return "", err
	}
	sections := splitInnoDBStatus(status)
	var b strings.Builder
	for _, want := range a.Sections {
		body, ok := sections[strings.ToUpper(want)]
		if !ok {
			fmt.Fprintf(&b, "## %s\n(not present)\n\n", want)
			continue
		}
		if ts.opts.Redact {
			// Record dumps contain row data; SQL lines contain literal values.
			body = recordDump.ReplaceAllString(body, "${1}<redacted>")
			body = sqlLine.ReplaceAllStringFunc(body, func(l string) string { return ts.sqlText(l, 400) })
		}
		fmt.Fprintf(&b, "## %s\n%s\n\n", want, toolset.Clip(body, 6000))
	}
	return b.String(), nil
}

var statementOrders = []string{"total_latency", "avg_latency", "max_latency", "exec_count", "rows_examined", "no_index", "lock_time", "tmp_disk_tables"}

func (ts *Toolset) topStatements(ctx context.Context, db *sql.DB, raw json.RawMessage) (string, error) {
	var a struct {
		OrderBy         string `json:"order_by"`
		Schema          string `json:"schema"`
		LastSeenMinutes int    `json:"last_seen_minutes"`
		Limit           int    `json:"limit"`
	}
	if err := toolset.DecodeArgs(raw, &a); err != nil {
		return "", err
	}
	if a.OrderBy == "" {
		a.OrderBy = "total_latency"
	}
	if !contains(statementOrders, a.OrderBy) {
		return "", fmt.Errorf("unsupported order_by %q", a.OrderBy)
	}
	// The sort key is chosen by a bound parameter, so the statement text is constant.
	const q = `SELECT SCHEMA_NAME AS db, DIGEST_TEXT AS digest, COUNT_STAR AS execs,
		       ROUND(SUM_TIMER_WAIT/1e12, 3) AS total_s, ROUND(AVG_TIMER_WAIT/1e9, 3) AS avg_ms,
		       ROUND(MAX_TIMER_WAIT/1e9, 3) AS max_ms, ROUND(SUM_LOCK_TIME/1e12, 3) AS lock_s,
		       SUM_ROWS_EXAMINED AS rows_examined, SUM_ROWS_SENT AS rows_sent, SUM_ROWS_AFFECTED AS rows_affected,
		       SUM_NO_INDEX_USED AS no_index, SUM_CREATED_TMP_DISK_TABLES AS tmp_disk, SUM_ERRORS AS errors,
		       FIRST_SEEN AS first_seen, LAST_SEEN AS last_seen
		FROM performance_schema.events_statements_summary_by_digest
		WHERE DIGEST_TEXT IS NOT NULL
		  AND DIGEST_TEXT NOT LIKE '%performance\_schema%'
		  AND DIGEST_TEXT NOT LIKE '%information\_schema%'
		  AND DIGEST_TEXT NOT LIKE 'SHOW %'
		  AND DIGEST_TEXT NOT LIKE 'SET %'
		  AND DIGEST_TEXT NOT LIKE '%_sys_ . _%' -- matches the quoted "sys" schema
		  AND (? = '' OR SCHEMA_NAME = ?)
		  AND (? = 0 OR LAST_SEEN >= NOW() - INTERVAL ? MINUTE)
		ORDER BY CASE ?
		    WHEN 'avg_latency'     THEN AVG_TIMER_WAIT
		    WHEN 'max_latency'     THEN MAX_TIMER_WAIT
		    WHEN 'exec_count'      THEN COUNT_STAR
		    WHEN 'rows_examined'   THEN SUM_ROWS_EXAMINED
		    WHEN 'no_index'        THEN SUM_NO_INDEX_USED
		    WHEN 'lock_time'       THEN SUM_LOCK_TIME
		    WHEN 'tmp_disk_tables' THEN SUM_CREATED_TMP_DISK_TABLES
		    ELSE SUM_TIMER_WAIT
		END DESC
		LIMIT ?`
	header, rows, err := query(ctx, db, q,
		a.Schema, a.Schema, a.LastSeenMinutes, a.LastSeenMinutes, a.OrderBy, clampInt(a.Limit, 15, 100))
	if err != nil {
		return "", err
	}
	for _, r := range rows {
		r[1] = toolset.Clip(strings.Join(strings.Fields(r[1]), " "), 500)
	}
	var since string
	_ = db.QueryRowContext(ctx, "SELECT DATE_SUB(NOW(), INTERVAL VARIABLE_VALUE SECOND) FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Uptime'").Scan(&since)
	return fmt.Sprintf("## top statement digests by %s (cumulative since %s unless reset)\n", a.OrderBy, since) +
		toolset.Table(header, rows), nil
}

// Counters reported as rates, and gauges reported as-is.
var (
	keyCounters = []string{
		"Questions", "Com_select", "Com_insert", "Com_update", "Com_delete", "Com_commit", "Com_rollback",
		"Innodb_rows_read", "Innodb_rows_inserted", "Innodb_rows_updated", "Innodb_rows_deleted",
		"Innodb_row_lock_waits", "Innodb_row_lock_time", "Innodb_buffer_pool_read_requests",
		"Innodb_buffer_pool_reads", "Innodb_buffer_pool_wait_free", "Innodb_data_fsyncs",
		"Innodb_os_log_written", "Created_tmp_disk_tables", "Created_tmp_tables", "Select_full_join",
		"Select_scan", "Sort_merge_passes", "Slow_queries", "Aborted_connects", "Aborted_clients",
		"Connection_errors_max_connections", "Threads_created", "Bytes_received", "Bytes_sent",
		"Handler_read_rnd_next",
	}
	gauges = []string{
		"Threads_running", "Threads_connected", "Innodb_row_lock_current_waits",
		"Innodb_buffer_pool_pages_dirty", "Innodb_buffer_pool_pages_free", "Open_tables",
	}
)

func globalStatus(ctx context.Context, db *sql.DB) (map[string]float64, error) {
	_, rows, err := query(ctx, db, "SHOW GLOBAL STATUS")
	if err != nil {
		return nil, err
	}
	m := make(map[string]float64, len(rows))
	for _, r := range rows {
		if v, err := strconv.ParseFloat(r[1], 64); err == nil {
			m[r[0]] = v
		}
	}
	return m, nil
}

func (ts *Toolset) statusDelta(ctx context.Context, db *sql.DB, raw json.RawMessage) (string, error) {
	var a struct {
		Interval int `json:"interval_seconds"`
	}
	if err := toolset.DecodeArgs(raw, &a); err != nil {
		return "", err
	}
	interval := time.Duration(clampInt(a.Interval, 5, 30)) * time.Second
	s1, err := globalStatus(ctx, db)
	if err != nil {
		return "", err
	}
	t1 := time.Now()
	select {
	case <-time.After(interval):
	case <-ctx.Done():
		return "", ctx.Err()
	}
	s2, err := globalStatus(ctx, db)
	if err != nil {
		return "", err
	}
	secs := time.Since(t1).Seconds()
	d := func(k string) float64 { return s2[k] - s1[k] }

	var b strings.Builder
	fmt.Fprintf(&b, "## rates over %.1fs window ending %s\n", secs, time.Now().UTC().Format(time.RFC3339))
	var rows [][]string
	for _, k := range keyCounters {
		if _, ok := s2[k]; ok {
			rows = append(rows, []string{k, fmt.Sprintf("%.1f", d(k)/secs)})
		}
	}
	b.WriteString(toolset.Table([]string{"counter", "per_second"}, rows))

	b.WriteString("\n## derived\n")
	derived := [][2]string{}
	if rr := d("Innodb_buffer_pool_read_requests"); rr > 0 {
		derived = append(derived, [2]string{"buffer_pool_hit_ratio", fmt.Sprintf("%.4f", 1-d("Innodb_buffer_pool_reads")/rr)})
	}
	if w := d("Innodb_row_lock_waits"); w > 0 {
		derived = append(derived, [2]string{"avg_row_lock_wait_ms", fmt.Sprintf("%.1f", d("Innodb_row_lock_time")/w)})
	}
	if len(derived) == 0 {
		derived = append(derived, [2]string{"note", "no buffer pool reads or row lock waits in window"})
	}
	b.WriteString(toolset.KV(derived))

	b.WriteString("\n## gauges (end of window)\n")
	var g [][]string
	for _, k := range gauges {
		if v, ok := s2[k]; ok {
			g = append(g, []string{k, strconv.FormatFloat(v, 'f', -1, 64)})
		}
	}
	var hll string
	if err := db.QueryRowContext(ctx, "SELECT `COUNT` FROM information_schema.INNODB_METRICS WHERE NAME = 'trx_rseg_history_len'").Scan(&hll); err == nil {
		g = append(g, []string{"history_list_length", hll})
	}
	b.WriteString(toolset.Table([]string{"gauge", "value"}, g))

	// Other busy counters the key list does not cover.
	key := map[string]bool{}
	for _, k := range keyCounters {
		key[k] = true
	}
	for _, k := range gauges {
		key[k] = true
	}
	type kv struct {
		k string
		v float64
	}
	var other []kv
	for k := range s2 {
		if key[k] || k == "Uptime" || k == "Uptime_since_flush_status" {
			continue
		}
		if r := d(k) / secs; r > 0 {
			other = append(other, kv{k, r})
		}
	}
	sort.Slice(other, func(i, j int) bool { return other[i].v > other[j].v })
	if len(other) > 15 {
		other = other[:15]
	}
	var orows [][]string
	for _, o := range other {
		orows = append(orows, []string{o.k, fmt.Sprintf("%.1f", o.v)})
	}
	b.WriteString("\n## other changing counters\n")
	b.WriteString(toolset.Table([]string{"counter", "per_second"}, orows))
	return b.String(), nil
}

func (ts *Toolset) replication(ctx context.Context, db *sql.DB, _ json.RawMessage) (string, error) {
	var b strings.Builder
	header, rows, err := query(ctx, db, "SHOW REPLICA STATUS")
	if err != nil {
		header, rows, err = query(ctx, db, "SHOW SLAVE STATUS")
	}
	b.WriteString("## replica status\n")
	switch {
	case err != nil:
		fmt.Fprintf(&b, "unavailable: %v\n", err)
	case len(rows) == 0:
		b.WriteString("this server is not a replica\n")
	default:
		fields := []string{
			"Channel_Name", "Source_Host", "Master_Host", "Source_Port", "Master_Port",
			"Replica_IO_Running", "Slave_IO_Running", "Replica_SQL_Running", "Slave_SQL_Running",
			"Seconds_Behind_Source", "Seconds_Behind_Master", "Replica_SQL_Running_State", "Slave_SQL_Running_State",
			"Last_IO_Errno", "Last_IO_Error", "Last_IO_Error_Timestamp", "Last_SQL_Errno", "Last_SQL_Error",
			"Last_SQL_Error_Timestamp", "Relay_Log_Space", "SQL_Delay", "Auto_Position",
			"Retrieved_Gtid_Set", "Executed_Gtid_Set",
		}
		for _, r := range rows {
			byName := map[string]string{}
			for i, h := range header {
				byName[h] = r[i]
			}
			var pairs [][2]string
			for _, f := range fields {
				if v, ok := byName[f]; ok {
					pairs = append(pairs, [2]string{f, toolset.Clip(v, 400)})
				}
			}
			b.WriteString(toolset.KV(pairs))
			b.WriteString("\n")
		}
		h, w, err := query(ctx, db, `SELECT CHANNEL_NAME, WORKER_ID, LAST_ERROR_NUMBER, LAST_ERROR_MESSAGE, LAST_ERROR_TIMESTAMP
			FROM performance_schema.replication_applier_status_by_worker WHERE LAST_ERROR_NUMBER <> 0`)
		if err == nil && len(w) > 0 {
			b.WriteString("## applier worker errors\n")
			b.WriteString(toolset.Table(h, w))
		}
	}

	b.WriteString("\n## source side\n")
	header, rows, err = query(ctx, db, "SHOW BINARY LOG STATUS")
	if err != nil {
		header, rows, err = query(ctx, db, "SHOW MASTER STATUS")
	}
	if err != nil {
		fmt.Fprintf(&b, "binary log status unavailable: %v\n", err)
	} else {
		b.WriteString(toolset.Table(header, rows))
	}
	_, reps, err := query(ctx, db, `SELECT HOST, TIME, STATE FROM performance_schema.processlist WHERE COMMAND LIKE 'Binlog Dump%'`)
	if err == nil {
		b.WriteString("connected replicas:\n")
		b.WriteString(toolset.Table([]string{"host", "time_s", "state"}, reps))
	}
	return b.String(), nil
}

func (ts *Toolset) errorLog(ctx context.Context, db *sql.DB, raw json.RawMessage) (string, error) {
	var a struct {
		SinceMinutes int    `json:"since_minutes"`
		MinPriority  string `json:"min_priority"`
		Limit        int    `json:"limit"`
	}
	if err := toolset.DecodeArgs(raw, &a); err != nil {
		return "", err
	}
	// Severity rank in FIELD() order below: Error=1, Warning=2, System=3, Note=4.
	ranks := map[string]int{"Error": 1, "Warning": 2, "Note": 4, "": 2}
	rank, ok := ranks[a.MinPriority]
	if !ok {
		return "", fmt.Errorf("unsupported min_priority %q", a.MinPriority)
	}
	header, rows, err := query(ctx, db, `SELECT LOGGED, PRIO, ERROR_CODE, SUBSYSTEM, DATA
		FROM performance_schema.error_log
		WHERE LOGGED >= NOW() - INTERVAL ? MINUTE
		  AND FIELD(PRIO, 'Error', 'Warning', 'System', 'Note') BETWEEN 1 AND ?
		ORDER BY LOGGED DESC LIMIT ?`, clampInt(a.SinceMinutes, 60, 10080), rank, clampInt(a.Limit, 50, 500))
	if err != nil {
		return "", err
	}
	for _, r := range rows {
		r[4] = toolset.Clip(r[4], 500)
	}
	return "## error log (newest first)\n" + toolset.Table(lower(header), rows), nil
}

func (ts *Toolset) variables(ctx context.Context, db *sql.DB, raw json.RawMessage) (string, error) {
	var a struct {
		Names   []string `json:"names"`
		Pattern string   `json:"pattern"`
	}
	if err := toolset.DecodeArgs(raw, &a); err != nil {
		return "", err
	}
	var rows [][]string
	if a.Pattern != "" {
		_, r, err := query(ctx, db, "SELECT VARIABLE_NAME, VARIABLE_VALUE FROM performance_schema.global_variables WHERE VARIABLE_NAME LIKE ? ORDER BY VARIABLE_NAME", a.Pattern)
		if err != nil {
			return "", err
		}
		rows = append(rows, r...)
	}
	if len(a.Names) > 0 {
		for _, n := range a.Names {
			if !identRe.MatchString(n) {
				return "", fmt.Errorf("invalid variable name %q", n)
			}
		}
		_, r, err := query(ctx, db, `SELECT VARIABLE_NAME, VARIABLE_VALUE FROM performance_schema.global_variables
			WHERE FIND_IN_SET(VARIABLE_NAME, ?) ORDER BY VARIABLE_NAME`, strings.Join(a.Names, ","))
		if err != nil {
			return "", err
		}
		rows = append(rows, r...)
	}
	if a.Pattern == "" && len(a.Names) == 0 {
		return "", fmt.Errorf("give names or pattern")
	}
	if len(rows) > 200 {
		rows = rows[:200]
	}
	return toolset.Table([]string{"variable", "value"}, rows), nil
}

func (ts *Toolset) tableStats(ctx context.Context, db *sql.DB, raw json.RawMessage) (string, error) {
	var a struct {
		Schema string `json:"schema"`
		Table  string `json:"table"`
	}
	if err := toolset.DecodeArgs(raw, &a); err != nil {
		return "", err
	}
	if _, err := quoteIdent(a.Schema); err != nil {
		return "", err
	}
	var b strings.Builder
	q := `SELECT TABLE_NAME AS table_name, ENGINE AS engine, TABLE_ROWS AS rows_est,
		       ROUND(DATA_LENGTH/1048576, 1) AS data_mb, ROUND(INDEX_LENGTH/1048576, 1) AS index_mb,
		       ROUND(DATA_FREE/1048576, 1) AS free_mb, AUTO_INCREMENT AS auto_inc,
		       CREATE_TIME AS created, UPDATE_TIME AS updated
		FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE'`
	args := []any{a.Schema}
	if a.Table != "" {
		if _, err := quoteIdent(a.Table); err != nil {
			return "", err
		}
		q += " AND TABLE_NAME = ?"
		args = append(args, a.Table)
	}
	q += " ORDER BY DATA_LENGTH + INDEX_LENGTH DESC LIMIT 20"
	header, rows, err := query(ctx, db, q, args...)
	if err != nil {
		return "", err
	}
	b.WriteString("## tables (statistics are estimates)\n")
	b.WriteString(toolset.Table(header, rows))
	if len(rows) == 0 {
		b.WriteString("no visible tables: the schema may not exist or this account lacks privileges on it\n")
	}
	if a.Table == "" || len(rows) == 0 {
		return b.String(), nil
	}

	header, rows, err = query(ctx, db, `SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY, EXTRA
		FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION`, a.Schema, a.Table)
	if err == nil {
		b.WriteString("\n## columns\n")
		b.WriteString(toolset.Table(lower(header), rows))
	}
	header, rows, err = query(ctx, db, `SELECT INDEX_NAME AS index_name, NON_UNIQUE AS non_unique,
		       GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX) AS columns, MAX(CARDINALITY) AS cardinality,
		       INDEX_TYPE AS type, IS_VISIBLE AS visible
		FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		GROUP BY INDEX_NAME, NON_UNIQUE, INDEX_TYPE, IS_VISIBLE ORDER BY INDEX_NAME`, a.Schema, a.Table)
	if err == nil {
		b.WriteString("\n## indexes\n")
		b.WriteString(toolset.Table(header, rows))
	}
	header, rows, err = query(ctx, db, `SELECT IFNULL(INDEX_NAME, '(none: table scan)') AS index_name,
		       COUNT_READ AS reads, COUNT_WRITE AS writes, ROUND(SUM_TIMER_WAIT/1e12, 3) AS total_s
		FROM performance_schema.table_io_waits_summary_by_index_usage
		WHERE OBJECT_SCHEMA = ? AND OBJECT_NAME = ? ORDER BY COUNT_STAR DESC`, a.Schema, a.Table)
	if err == nil {
		b.WriteString("\n## index usage since startup\n")
		b.WriteString(toolset.Table(header, rows))
	}
	return b.String(), nil
}

// explain shows the optimizer's plan for a statement the model supplies.
// It is registered only when safety.allow_explain is true.
//
// Typical use: mysql_top_statements surfaces a suspicious digest (many rows
// examined, no index used), mysql_table_stats shows the table's indexes, and
// explain confirms how the statement is executed.
//
// Current design and its limits:
//
//   - The statement comes from the model, which fills the digest's '?'
//     placeholders with representative values. This is the main injection
//     surface of the toolset; see guardedRun for the layers that contain it.
//     Escaping the statement is not a fix: it would turn the SQL into a string
//     literal and EXPLAIN would fail with a syntax error.
//   - Only SELECT/WITH/TABLE can be explained. MySQL requires the privilege of
//     the explained statement (EXPLAIN UPDATE needs UPDATE, error 1142), and
//     the read-only transaction rejects it anyway (error 1792). Granting write
//     privileges to see a plan is not acceptable, so writes are refused with
//     guidance to explain the equivalent SELECT instead.
//   - Plans of writes matter for lock incidents: under REPEATABLE READ an
//     UPDATE/DELETE locks every row it scans, so the access path of its WHERE
//     clause decides whether it locks ten rows or the whole table.
//
// TODO(explain-by-digest): stop accepting SQL from the model.
//
//  1. mysql_top_statements also returns each digest's DIGEST hash.
//  2. mysql_explain takes only a digest (validated as hex) and loads
//     QUERY_SAMPLE_TEXT, a statement the server really executed, from
//     performance_schema.events_statements_summary_by_digest.
//  3. Parse it with a real MySQL parser (github.com/pingcap/tidb/pkg/parser;
//     not regexes) and, for writes, rewrite to the SELECT that locates the
//     same rows: UPDATE/DELETE keep FROM/JOIN/WHERE/ORDER BY/LIMIT,
//     INSERT ... SELECT keeps its SELECT, INSERT ... VALUES has no plan worth
//     showing. Label the output as the plan of the rewritten query.
//  4. Redact literal values in the plan output (sample text carries real
//     data) while keeping cost and row estimates.
//
// The same parser can then replace the lexer-based GuardReadOnly with a
// syntax-tree check. Once the model no longer supplies SQL, allow_explain can
// default to true.
func (ts *Toolset) explain(ctx context.Context, db *sql.DB, raw json.RawMessage) (string, error) {
	var a struct {
		Schema    string `json:"schema"`
		Statement string `json:"statement"`
	}
	if err := toolset.DecodeArgs(raw, &a); err != nil {
		return "", err
	}
	// Answer writes with guidance before the generic guard error, so the model
	// knows how to get the plan it wants.
	if f := strings.Fields(a.Statement); len(f) > 0 {
		switch w := strings.ToUpper(f[0]); w {
		case "UPDATE", "DELETE", "INSERT", "REPLACE":
			return "", fmt.Errorf("%s cannot be explained with a read-only account (MySQL requires %s privilege). "+
				"Explain a SELECT with the same FROM/JOIN/WHERE/ORDER BY/LIMIT to see how the rows are located; "+
				"under REPEATABLE READ every row that SELECT scans would be locked by the %s", w, w, w)
		}
	}
	stmt, err := GuardReadOnly(a.Statement)
	if err != nil {
		return "", err
	}
	lead := strings.ToUpper(strings.Fields(stmt)[0])
	if lead != "SELECT" && lead != "WITH" && lead != "TABLE" && !strings.HasPrefix(lead, "(") {
		return "", fmt.Errorf("mysql_explain takes a SELECT statement; got %s", lead)
	}
	var out string
	err = guardedRun(ctx, db, a.Schema, func(conn *sql.Conn) error {
		// The model-supplied statement is the one deliberate exception to
		// constant SQL: it passed GuardReadOnly and runs inside a read-only
		// transaction on a read-only session, without multi-statement support.
		_, tree, _, err := queryLimit(ctx, conn, "EXPLAIN FORMAT=TREE "+stmt, 1) // #nosec G202 -- guarded, see above
		if err == nil && len(tree) == 1 {
			out = fmt.Sprintf("## query plan (tree format, estimates only)\n%s\n", tree[0][0])
			return nil
		}
		header, rows, _, err := queryLimit(ctx, conn, "EXPLAIN "+stmt, 200) // #nosec G202 -- guarded, see above
		if err != nil {
			return err
		}
		out = fmt.Sprintf("## query plan (traditional format)\n%s", toolset.Table(header, rows))
		return nil
	})
	return out, err
}

func (ts *Toolset) rawQuery(ctx context.Context, db *sql.DB, raw json.RawMessage) (string, error) {
	var a struct {
		Statement string `json:"statement"`
		Schema    string `json:"schema"`
	}
	if err := toolset.DecodeArgs(raw, &a); err != nil {
		return "", err
	}
	stmt, err := GuardReadOnly(a.Statement)
	if err != nil {
		return "", err
	}
	const maxRows = 200
	var out string
	err = guardedRun(ctx, db, a.Schema, func(conn *sql.Conn) error {
		header, rows, more, err := queryLimit(ctx, conn, stmt, maxRows) // #nosec G202 -- guarded, see guardedRun
		if err != nil {
			return err
		}
		for _, r := range rows {
			for i := range r {
				r[i] = toolset.Clip(r[i], 300)
			}
		}
		if more {
			out = fmt.Sprintf("(first %d rows; more were not read)\n", maxRows)
		}
		out += toolset.Table(header, rows)
		return nil
	})
	return out, err
}

// guardedRun executes model-supplied SQL. It is only called with statements
// that passed GuardReadOnly, and adds independent server-side layers:
//   - a dedicated connection whose session is transaction_read_only=ON with
//     max_execution_time (set when the pool opens it, see open);
//   - an explicit START TRANSACTION READ ONLY that is always rolled back;
//   - the driver's default of no multi-statement support, so a second
//     statement is a syntax error on the server.
//
// The account's privileges (docs/mysql-privileges.sql) are the final layer.
func guardedRun(ctx context.Context, db *sql.DB, schema string, fn func(*sql.Conn) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if schema != "" {
		// Identifiers cannot be bound as parameters; quoteIdent only admits
		// [A-Za-z0-9_$] and backtick-quotes the result.
		q, err := quoteIdent(schema)
		if err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "USE "+q); err != nil { // #nosec G202 -- validated identifier
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, "START TRANSACTION READ ONLY"); err != nil {
		return err
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()
	return fn(conn)
}

func lower(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = strings.ToLower(x)
	}
	return out
}
