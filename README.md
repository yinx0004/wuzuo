<p align="center">
  <img src="docs/assets/banner.webp" width="360" alt="Yuanfang: 元芳 looking through a magnifier, asking 元芳，你怎么看？">
</p>

<p align="center">
  AI SRE forensics agent<br>
  <b>Every incident leaves evidence.</b>
</p>

<p align="center">
  An open-source AI agent for production incident investigation and root cause analysis.
</p>

<p align="center">
  <a href="https://github.com/yinx0004/yuanfang/actions/workflows/ci.yml"><img src="https://github.com/yinx0004/yuanfang/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="License"></a>
  <a href="README.zh-CN.md">中文文档</a>
</p>

---

**Yuanfang** (元芳, *yuán-fāng*) is named after Li Yuanfang, Judge Dee's lieutenant in
Chinese detective stories. Whenever a case turned murky, Judge Dee asked him "Yuanfang, what
do you make of it?" (元芳，你怎么看？), a line most Chinese internet users know by heart.
Yuanfang answers that question for database incidents, and answers it with evidence:

- It first lists the plausible causes, then runs read-only probes to confirm or rule out
  each one.
- Each probe result is saved as numbered evidence (`E1`, `E2`, …), with SQL values and
  credentials redacted and a SHA-256 hash, so later edits are detected.
- The final diagnosis report has to cite that evidence. Yuanfang checks every citation
  and sends the report back if it cites evidence that does not exist. A cause with no
  evidence can only be reported as low confidence.
- Probes connect with a least-privilege account, on sessions set to
  `transaction_read_only=ON` with a time limit. Yuanfang suggests fixes but never runs them.
- `yuanfang case replay` re-runs the analysis on saved evidence, for example with another
  model, without connecting to production again.
- It is graded, not just demoed: `make eval` stages incidents in a lab, records the ground
  truth, and checks whether Yuanfang named the right cause with evidence that actually shows
  it ([details](#evaluate-on-staged-incidents)).

> Status: early development (v0.1). MySQL is supported now; MongoDB comes next.

## How an investigation looks

Illustrative output from the lab's `lock-wait` scenario:

```text
$ yuanfang ask "updates to orders are hanging since a minute ago" -t prod-orders
case 20261101-055624-3f2a, model anthropic/claude-opus-5-5 (built-in alias "opus")
  hypothesis  H1 Row lock contention on shop.orders
  hypothesis  H2 Metadata lock from DDL
  hypothesis  H3 Server saturation (CPU/IO/connections)
  probe       mysql_lock_waits {"target":"prod-orders"}
  probe       mysql_transactions {"target":"prod-orders"}
  probe       mysql_status_delta {"interval_seconds":5,"target":"prod-orders"}
  evidence    E1 mysql_lock_waits saved
  evidence    E2 mysql_transactions saved
  evidence    E3 mysql_status_delta saved
  ruled out   H2 Metadata lock from DDL: no metadata lock waits
  ruled out   H3 Server saturation: Threads_running 7, no buffer pool misses
closed in 41s: 4 steps, 3 evidence, 48211 in / 2390 out tokens, saved to ~/.yuanfang/cases/20261101-055624-3f2a
```

The report is printed as Markdown, or as JSON with `--json`. It is also saved in the
case directory:

```markdown
# Orders updates blocked by an idle transaction holding a row lock

## Root Cause
Session 48 (app@10.0.3.17) ran `UPDATE orders SET status = ?, note = ? WHERE id = ?`
inside a transaction 5 minutes ago and has been idle (Sleep) since without committing.
It holds an X record lock on orders.PRIMARY that every later update of that row waits for.
*confidence: high* `[E1, E2]`

## Ruled Out
- ~~Metadata lock from DDL~~: no metadata lock waits `[E1]`
- ~~Server saturation~~: 7 running threads, buffer pool hit ratio 0.9999 `[E3]`

## Recommendations
1. Identify the owner of session 48 and commit or roll back; as a last resort `KILL 48`. *(risk: medium)*
2. Set a transaction timeout in the application's connection pool. *(risk: low)*
```

## Quick start

### 1. Install

```bash
brew install yinx0004/tap/yuanfang                       # macOS and Linux
```

```bash
go install github.com/yinx0004/yuanfang/cmd/yuanfang@latest  # with Go
```

Each release also publishes binaries for Linux, macOS and Windows on the
[releases page](https://github.com/yinx0004/yuanfang/releases), and an image at
`ghcr.io/yinx0004/yuanfang`. Homebrew gets stable releases only, not pre-releases.

### 2. Set up

Run `yuanfang`. On first use it walks you through choosing a model, entering its API key,
and adding a MySQL target (host, port, user, password), then tests the connection. Settings go to
`~/.yuanfang/config.yaml`; the key and DSN go to `~/.yuanfang/credentials` (mode 0600), and
exported environment variables take precedence over that file. The rest of this
section describes the same settings by hand.

### 3. Choose a model

Yuanfang detects a provider from the environment. You do not have to configure anything
when one of these is set:

| Environment | Provider | Default model ([short name](docs/configuration.md#short-names)) |
|---|---|---|
| `ANTHROPIC_API_KEY` | Anthropic Claude (native API) | `opus` |
| `OPENAI_API_KEY` + `OPENAI_MODEL` (+ `OPENAI_BASE_URL`) | OpenAI or any compatible endpoint: Ollama, vLLM, LiteLLM, Bifrost… | — |
| `DEEPSEEK_API_KEY` | DeepSeek | `deepseek` |
| `DASHSCOPE_API_KEY` | Qwen (Alibaba Cloud) | `qwen` |
| `ARK_API_KEY` + `ARK_MODEL` | Doubao (Volcengine Ark) | — |

Pick a model by short name: `-m opus`, `-m sonnet`, `-m deepseek`, `-m qwen`, or
`-m provider/model-id`. `yuanfang models` lists the names. If nothing is configured,
`yuanfang ask` lets you choose from the models whose key is set and offers to save the
choice.

### 4. Create a read-only account and point Yuanfang at it

```sql
-- docs/mysql-privileges.sql
CREATE USER 'yuanfang'@'%' IDENTIFIED BY RANDOM PASSWORD;  -- prints the generated password once
GRANT PROCESS, REPLICATION CLIENT ON *.* TO 'yuanfang'@'%';
GRANT SELECT ON performance_schema.* TO 'yuanfang'@'%';
GRANT SELECT, EXECUTE ON sys.* TO 'yuanfang'@'%';
```

```bash
export YUANFANG_MYSQL_DSN='yuanfang:...@tcp(10.0.0.5:3306)/'
yuanfang toolset check          # connectivity, privileges, server features
yuanfang ask "the orders database got slow after 10:00"
```

For several servers, run `yuanfang init` and list them under `targets:`. Each target names the
environment variable that holds its DSN, so credentials never live in the config file.
See **[docs/configuration.md](docs/configuration.md)** for where the config file lives,
every field and environment variable, model selection rules, and troubleshooting.

## Try it on a staged incident

`lab/` is a local test environment for development and demos, not something to deploy:
a disposable MySQL 8.4 in Docker with a seeded `shop` schema, plus scripts that stage
real incidents. Never point the scripts at a real server.

```bash
make lab-up && make build
eval "$(make -s lab-env)"
lab/scenarios/lock-wait.sh        # also: slow-query.sh, deadlock.sh, conn-pileup.sh
./bin/yuanfang ask "updates to orders are hanging" -t lab
lab/scenarios/reset.sh                  # end the staged incident
make lab-stop                           # pause the lab, keep its data
make lab-destroy                        # remove it completely
```

### Evaluate on staged incidents

`make eval` grades Yuanfang on lab incidents automatically. Each run restarts the lab MySQL,
stages the incident, records the ground truth (for lock wait, the real blocking and waiting
connection IDs), runs `yuanfang ask` with only the complaint, and grades the report with
rules: did it name the right cause, does the cited evidence show it, and is every
recommendation safe. A run ends in `PASS`, `FAIL`, or `ERROR` when the lab itself
misbehaved. It calls the model for real, so it costs tokens.

```bash
make eval EVAL_ARGS="-scenario lock-wait -runs 3"   # results in eval/results/<time>/
```

Scenarios so far: `lock-wait`. More are tracked in
[#15](https://github.com/yinx0004/yuanfang/issues/15); details in [eval/README.md](eval/README.md).

## Commands

| Command | |
|---|---|
| `yuanfang ask <problem>` | Investigate and print the diagnosis report (`-m model`, `-t target`, `--json`, `-q`) |
| `yuanfang case list \| show <id>` | Browse past cases (ID prefixes work) |
| `yuanfang case show <id> --transcript` | Step by step: what the model saw, its thinking summary, the probes it called, and what came back (`--full` for unshortened text) |
| `yuanfang case replay <id>` | Re-examine sealed evidence, e.g. with another model: `-m sonnet` |
| `yuanfang models` | Model short names usable with `-m`, and which keys are set |
| `yuanfang models fetch` | Ask each provider which models it offers now; flags newer and retired ones |
| `yuanfang toolset list \| check` | List probes and check their prerequisites |
| `yuanfang toolset run <probe> [json]` | Run one probe by hand |
| `yuanfang init` | First-time setup: choose a model, add a target, write `~/.yuanfang/config.yaml` |

## MySQL probes

| Probe | Looks at |
|---|---|
| `mysql_overview` | version, role, key configuration, connection and thread counts |
| `mysql_processlist` | sessions by age, with a summary by user, command and state |
| `mysql_transactions` | open InnoDB transactions, including idle ones and their last statement |
| `mysql_lock_waits` | row lock and metadata lock waits, plus the root blocker of each chain |
| `mysql_innodb_status` | deadlocks, history list length and semaphores, with row dumps redacted |
| `mysql_top_statements` | statement digests ranked by latency, executions, rows examined or missing index |
| `mysql_status_delta` | per-second rates from two status samples, plus gauges |
| `mysql_replication` | replica threads, lag and errors; binlog status and connected replicas |
| `mysql_error_log` | `performance_schema.error_log` |
| `mysql_variables` | global variables by name or pattern |
| `mysql_table_stats` | table sizes, columns, indexes and index usage |
| `mysql_explain` | `EXPLAIN` of a SELECT (never `EXPLAIN ANALYZE`); off by default (`safety.allow_explain`) |
| `mysql_query` | guarded free-form read-only query; off by default (`safety.allow_raw_sql`) |

## Safety model

Yuanfang connects to production databases, so staying read-only cannot depend on the model
following instructions. Each of these controls blocks writes on its own:

| Control | What it does |
|---|---|
| Database account | Least-privilege grants (see above). `yuanfang toolset check` warns about accounts with `ALL PRIVILEGES`. |
| Session | Every connection sets `transaction_read_only=ON`, `max_execution_time` and `lock_wait_timeout=5`. At most 3 connections per target. |
| Probes | Built-in probes use constant SQL with bound parameters. |
| Model-written SQL | Only `mysql_explain` and `mysql_query` accept SQL from the model, and both are off by default (`safety.allow_explain`, `safety.allow_raw_sql`). The statement must pass a guard that allows a single SELECT, SHOW, EXPLAIN or DESCRIBE and rejects locking reads, `SLEEP`, `INTO OUTFILE`, executable comments, and syntax the guard and the server could read differently. It then runs inside `START TRANSACTION READ ONLY`, which is always rolled back. |
| Output | SQL values become `?`, InnoDB row dumps are removed, and credentials are stripped before anything is shown to the model or written to disk. |
| Fixes | Recommendations are text for a human. Yuanfang cannot execute them. |

An integration test sends UPDATE, CREATE TABLE and multi-statement SQL past the guard
using an account with every privilege; the session and transaction settings reject all
of them.

Built-in probes never select table rows. `mysql_query` can, if you enable it and also
grant the Yuanfang account SELECT on application schemas.

## Architecture

```text
cmd/yuanfang            CLI (cobra)
internal/agent       investigation loop: hypotheses → probes → rule-outs → diagnosis
internal/llm         provider-neutral chat + tools (Anthropic native, OpenAI-compatible)
internal/evidence    evidence locker: numbering, redaction, sealing, persistence
internal/report      diagnosis schema, validation against evidence, Markdown rendering
internal/cases       case storage under ~/.yuanfang/cases
pkg/toolset          public toolset/probe interface for extensions
pkg/redact           SQL literal and secret redaction
toolsets/mysql       MySQL 8.0+ probes and the read-only guard
lab                  local test lab for development: disposable MySQL plus incident scripts
eval                 evaluation: stages lab incidents and grades the diagnoses
```

## Roadmap

- **[v0.1.0](https://github.com/yinx0004/yuanfang/milestone/1)**: MySQL toolset, with an
  evaluation on staged lab incidents
- **[v0.2.0](https://github.com/yinx0004/yuanfang/milestone/2)**: MongoDB toolset, more MySQL
  coverage
- Later: Prometheus and other data sources, an HTTP server, chat bots, alert-driven
  investigations

All planned work is tracked as [issues](https://github.com/yinx0004/yuanfang/issues), labelled
by priority (`priority/critical`, `high`, `medium`, `low`) and area.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security reports: [SECURITY.md](SECURITY.md).

## License

[Apache License 2.0](LICENSE)
