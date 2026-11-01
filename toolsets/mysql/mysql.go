// SPDX-License-Identifier: Apache-2.0

// Package mysql is Yuanfang's MySQL toolset: read-only probes over
// performance_schema, sys, information_schema and SHOW statements for
// MySQL 8.0 and later.
//
// Every connection is opened with transaction_read_only=ON, a statement time
// limit and a short lock_wait_timeout, and the pool is kept small so that an
// investigation never adds meaningful load to a struggling server.
package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	driver "github.com/go-sql-driver/mysql"

	"github.com/yinx0004/yuanfang/pkg/redact"
	"github.com/yinx0004/yuanfang/pkg/toolset"
)

// Target is one MySQL server.
type Target struct {
	Name        string
	DSN         string
	Description string
}

// Options configures the toolset.
type Options struct {
	Targets      []Target
	QueryTimeout time.Duration
	// Redact normalizes literals in SQL text and strips record dumps.
	Redact bool
	// AllowExplain exposes mysql_explain, which explains a SELECT written by
	// the model.
	AllowExplain bool
	// AllowRawSQL exposes mysql_query for guarded free-form read-only queries.
	AllowRawSQL bool
}

// Toolset implements toolset.Toolset for MySQL.
type Toolset struct {
	opts    Options
	targets map[string]Target
	mu      sync.Mutex
	dbs     map[string]*sql.DB
}

var _ toolset.Toolset = (*Toolset)(nil)

// New creates the toolset. Connections are opened lazily.
func New(opts Options) (*Toolset, error) {
	if len(opts.Targets) == 0 {
		return nil, fmt.Errorf("mysql: no targets")
	}
	if opts.QueryTimeout <= 0 {
		opts.QueryTimeout = 10 * time.Second
	}
	ts := &Toolset{opts: opts, targets: map[string]Target{}, dbs: map[string]*sql.DB{}}
	for _, t := range opts.Targets {
		if _, err := driver.ParseDSN(t.DSN); err != nil {
			return nil, fmt.Errorf("mysql target %q: invalid DSN: %v", t.Name, redact.Secrets(err.Error()))
		}
		ts.targets[t.Name] = t
	}
	return ts, nil
}

// Name implements toolset.Toolset.
func (ts *Toolset) Name() string { return "mysql" }

// Description implements toolset.Toolset.
func (ts *Toolset) Description() string {
	return "MySQL 8.0+ sessions, transactions, locks, statements, InnoDB, replication and error log"
}

// Close implements toolset.Toolset.
func (ts *Toolset) Close() error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, db := range ts.dbs {
		_ = db.Close()
	}
	ts.dbs = map[string]*sql.DB{}
	return nil
}

func (ts *Toolset) targetNames() []string {
	names := make([]string, 0, len(ts.targets))
	for n := range ts.targets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// conn returns the pool for a target, resolving an empty name when there is
// exactly one target.
func (ts *Toolset) conn(name string) (*sql.DB, string, error) {
	if name == "" {
		if len(ts.targets) != 1 {
			return nil, "", fmt.Errorf("target is required (one of: %s)", strings.Join(ts.targetNames(), ", "))
		}
		name = ts.targetNames()[0]
	}
	t, ok := ts.targets[name]
	if !ok {
		return nil, "", fmt.Errorf("unknown mysql target %q (one of: %s)", name, strings.Join(ts.targetNames(), ", "))
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if db, ok := ts.dbs[name]; ok {
		return db, name, nil
	}
	db, err := open(t.DSN, ts.opts.QueryTimeout)
	if err != nil {
		return nil, name, err
	}
	ts.dbs[name] = db
	return db, name, nil
}

func open(dsn string, timeout time.Duration) (*sql.DB, error) {
	cfg, err := driver.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = timeout + 35*time.Second // status sampling may wait up to 30s
	}
	cfg.MultiStatements = false
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	// Session guard rails, applied by the driver with SET on every new connection.
	cfg.Params["transaction_read_only"] = "1"
	cfg.Params["max_execution_time"] = strconv.FormatInt(timeout.Milliseconds(), 10)
	cfg.Params["lock_wait_timeout"] = "5"
	connector, err := driver.NewConnector(cfg)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(time.Minute)
	return db, nil
}

// Check implements toolset.Toolset.
func (ts *Toolset) Check(ctx context.Context) []toolset.CheckResult {
	var out []toolset.CheckResult
	for _, name := range ts.targetNames() {
		out = append(out, ts.checkTarget(ctx, name)...)
	}
	return out
}

func (ts *Toolset) checkTarget(ctx context.Context, name string) []toolset.CheckResult {
	ctx, cancel := context.WithTimeout(ctx, ts.opts.QueryTimeout)
	defer cancel()
	res := func(item string, ok bool, detail string) toolset.CheckResult {
		return toolset.CheckResult{Target: name, Item: item, OK: ok, Detail: detail}
	}
	db, _, err := ts.conn(name)
	if err != nil {
		return []toolset.CheckResult{res("connect", false, err.Error())}
	}
	var version string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return []toolset.CheckResult{res("connect", false, redact.Secrets(err.Error()))}
	}
	out := []toolset.CheckResult{res("connect", true, "MySQL "+version)}
	major, _ := strconv.Atoi(strings.SplitN(version, ".", 2)[0])
	out = append(out, res("version >= 8.0", major >= 8, version))

	var ro int
	err = db.QueryRowContext(ctx, "SELECT @@transaction_read_only").Scan(&ro)
	out = append(out, res("read-only session", err == nil && ro == 1, errDetail(err, "transaction_read_only=ON")))

	var ps int
	err = db.QueryRowContext(ctx, "SELECT @@performance_schema").Scan(&ps)
	out = append(out, res("performance_schema enabled", err == nil && ps == 1, errDetail(err, "")))

	grants := ""
	if _, rows, err := query(ctx, db, "SHOW GRANTS"); err == nil {
		for _, r := range rows {
			grants += strings.ToUpper(r[0]) + "\n"
		}
	}
	all := strings.Contains(grants, "ALL PRIVILEGES ON *.*")
	out = append(out,
		res("PROCESS privilege", all || strings.Contains(grants, "PROCESS"), "needed for other sessions, INNODB_TRX and InnoDB status"),
		res("REPLICATION CLIENT privilege", all || strings.Contains(grants, "REPLICATION CLIENT"), "needed for replica status"),
	)
	if all {
		out = append(out, res("least privilege", false, "account has ALL PRIVILEGES; use a dedicated read-only account (see docs/mysql-privileges.sql)"))
	}

	probe := func(item, q string) {
		_, _, err := query(ctx, db, q)
		out = append(out, res(item, err == nil, errDetail(err, "")))
	}
	probe("read performance_schema", "SELECT 1 FROM performance_schema.events_statements_summary_by_digest LIMIT 1")
	probe("read sys schema", "SELECT 1 FROM sys.innodb_lock_waits LIMIT 1")

	var digest string
	err = db.QueryRowContext(ctx, "SELECT ENABLED FROM performance_schema.setup_consumers WHERE NAME = 'statements_digest'").Scan(&digest)
	out = append(out, res("statement digests enabled", err == nil && digest == "YES", errDetail(err, "")))
	return out
}

func errDetail(err error, ok string) string {
	if err != nil {
		return redact.Secrets(err.Error())
	}
	return ok
}

// probe is a single MySQL tool.
type probe struct {
	ts      *Toolset
	name    string
	desc    string
	props   map[string]any
	req     []string
	timeout time.Duration // extra time on top of QueryTimeout
	run     func(ctx context.Context, db *sql.DB, args json.RawMessage) (string, error)
}

func (p *probe) Spec() toolset.Spec {
	props := map[string]any{
		"target": map[string]any{"type": "string", "enum": p.ts.targetNames(), "description": "MySQL target to examine."},
	}
	for k, v := range p.props {
		props[k] = v
	}
	req := append([]string(nil), p.req...)
	if len(p.ts.targets) > 1 {
		req = append(req, "target")
	}
	return toolset.Spec{Name: p.name, Description: p.desc, Parameters: toolset.Schema{Properties: props, Required: req}}
}

func (p *probe) Run(ctx context.Context, raw json.RawMessage) (*toolset.Result, error) {
	var a struct {
		Target string `json:"target"`
	}
	if err := toolset.DecodeArgs(raw, &a); err != nil {
		return nil, err
	}
	db, name, err := p.ts.conn(a.Target)
	if err != nil {
		return &toolset.Result{Target: name}, err
	}
	ctx, cancel := context.WithTimeout(ctx, p.ts.opts.QueryTimeout+p.timeout)
	defer cancel()
	out, err := p.run(ctx, db, raw)
	if err != nil {
		err = fmt.Errorf("%s", redact.Secrets(err.Error()))
	}
	return &toolset.Result{Target: name, Content: out}, err
}

// query runs q and returns all values as strings (NULL as "NULL").
func query(ctx context.Context, db queryer, q string, args ...any) ([]string, [][]string, error) {
	cols, rows, _, err := queryLimit(ctx, db, q, 0, args...)
	return cols, rows, err
}

// queryLimit is query that stops reading after max rows (0 = no limit) and
// reports whether more rows were available.
func queryLimit(ctx context.Context, db queryer, q string, max int, args ...any) ([]string, [][]string, bool, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, false, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, false, err
	}
	var out [][]string
	for rows.Next() {
		if max > 0 && len(out) == max {
			return cols, out, true, nil
		}
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, false, err
		}
		row := make([]string, len(cols))
		for i, v := range vals {
			if v.Valid {
				row[i] = v.String
			} else {
				row[i] = "NULL"
			}
		}
		out = append(out, row)
	}
	return cols, out, false, rows.Err()
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// sqlText prepares statement text for display.
func (ts *Toolset) sqlText(s string, max int) string {
	if s == "NULL" || s == "" {
		return s
	}
	if ts.opts.Redact {
		s = redact.SQL(s)
	}
	return toolset.Clip(strings.Join(strings.Fields(s), " "), max)
}
