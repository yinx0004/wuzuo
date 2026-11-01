# Configuration

[中文](configuration.zh-CN.md)

WuZuo runs with **no configuration file at all**. Environment variables are enough.
A config file adds several targets, a fixed model, and safety settings.

- [Where the config file lives](#where-the-config-file-lives)
- [Running from environment variables only](#running-from-environment-variables-only)
- [Choosing the model](#choosing-the-model): short names, your own aliases, the first-run prompt
- [Targets](#targets)
- [Safety settings](#safety-settings)
- [Reference: all fields](#reference-all-fields)
- [Reference: environment variables](#reference-environment-variables)
- [Checking your setup](#checking-your-setup)
- [Troubleshooting](#troubleshooting)

## Where the config file lives

WuZuo uses the first of these that applies:

1. `--config` / `-c <path>` on the command line
2. `$WUZUO_CONFIG`
3. `$WUZUO_HOME/config.yaml`, where `$WUZUO_HOME` defaults to `~/.wuzuo`

A missing file at the default location (3) is fine: WuZuo falls back to environment
variables. A missing file named explicitly with 1 or 2 is an error.

`$WUZUO_HOME` also holds the case archive (`$WUZUO_HOME/cases`) unless `cases_dir` says
otherwise.

Create a starter file with:

```bash
wuzuo init            # writes ~/.wuzuo/config.yaml (mode 0600); --force overwrites
```

In a terminal, `init` lets you pick the default model from those whose API key is set.

## Running from environment variables only

```bash
export ANTHROPIC_API_KEY=...                              # or another provider, see below
export WUZUO_MYSQL_DSN='wuzuo:<password>@tcp(10.0.0.5:3306)/'
wuzuo toolset check
wuzuo ask "orders database got slow after 10:00"
```

When `WUZUO_MYSQL_DSN` is set and the config file defines no MySQL target, WuZuo adds a
target named `mysql` that reads its DSN from that variable.

## Choosing the model

### Short names

You never need to type a full model ID. Use a short name with `-m`, in `llm.model`,
or in `$WUZUO_MODEL`:

```bash
wuzuo ask -m sonnet "orders database got slow"
wuzuo case replay 20261101-0556 -m deepseek     # re-examine with another model
wuzuo models                                    # list names and which keys are set
```

Built-in names (each one fixes both the provider and the model):

| Name | Provider | Model |
|---|---|---|
| `opus` | anthropic | `claude-opus-5-5` |
| `sonnet` | anthropic | `claude-sonnet-5-5` |
| `haiku` | anthropic | `claude-haiku-5-5` |
| `fable` | anthropic | `claude-fable-5-1` |
| `deepseek` | deepseek | `deepseek-chat` |
| `deepseek-r` | deepseek | `deepseek-reasoner` |
| `qwen` | qwen | `qwen-plus` |
| `qwen-max` | qwen | `qwen-max` |

Built-in names follow the current generation and may move to newer models in later
releases. Each case records the full model ID it actually used.

A model name can also be:

- **your own alias** from `models:` in the config (see below); these override built-in
  names with the same name;
- **`provider/model-id`**, for any model a provider offers, e.g. `-m openai/gpt-5`,
  `-m ark/ep-20261101-xxxxx`;
- **a bare provider name**, e.g. `-m ark`, which uses that provider's model variable
  (`ARK_MODEL`, `OPENAI_MODEL`) or its default;
- **a raw model ID**, sent to the provider set by `llm.provider` (or detected), e.g.
  `qwen3:32b` for Ollama.

### Your own names

Give long or account-specific IDs a short name:

```yaml
models:
  doubao: { provider: ark, model: ep-20261101-xxxxx }
  local:  { provider: openai, model: qwen3:32b, base_url: http://localhost:11434/v1, api_key_env: OLLAMA_API_KEY }
  gw:     { provider: openai, model: gpt-5, base_url: https://llm-gateway.internal.example.com/v1, api_key_env: GATEWAY_TOKEN }

llm:
  model: doubao
```

Each alias takes `provider` and `model` (required), plus optional `base_url` and
`api_key_env`. Alias names may not contain spaces or `/`.

### When no model is configured

If none of `-m`, `$WUZUO_MODEL`, `$WUZUO_PROVIDER`, `llm.model` or `llm.provider` is set,
`wuzuo ask` and `wuzuo case replay` ask you to choose:

```text
No model is configured yet.
Choose a model for WuZuo:
  1)  opus    anthropic/claude-opus-5-5    Claude Opus, strongest default     (default)
  2)  sonnet  anthropic/claude-sonnet-5-5  Claude Sonnet, faster and cheaper
  3)  haiku   anthropic/claude-haiku-5-5   Claude Haiku, fastest
  4)  fable   anthropic/claude-fable-5-1   Claude Fable, most capable
Also supported once their key is set:
     qwen, qwen-max — set DASHSCOPE_API_KEY
     deepseek, deepseek-r — set DEEPSEEK_API_KEY
Model [1]: 2
Save "sonnet" as the default model in /home/you/.wuzuo/config.yaml? [Y/n]
```

- Only models whose API key is set are offered. Answer with a number, a name, or
  `provider/model-id`. Enter takes the default.
- The default is the default model of the first provider, in the order below, whose
  key is set.
- Saving writes `llm.model` to the config file, creating it from the starter template
  if needed. Other settings and comments in an existing file are kept.
- Without a terminal (CI, cron, pipes into stdin), WuZuo does not prompt. It uses the
  default and prints a note.

`wuzuo init` offers the same choice when it writes a new config file.

### Providers and keys

| Order | Provider | Key variable | Endpoint | Default model |
|---|---|---|---|---|
| 1 | `anthropic` | `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN` | Anthropic API, or `ANTHROPIC_BASE_URL` | `claude-opus-5-5` |
| 2 | `openai` | `OPENAI_API_KEY` | OpenAI, or `OPENAI_BASE_URL` | none: set `OPENAI_MODEL` or use `openai/<id>` |
| 3 | `deepseek` | `DEEPSEEK_API_KEY` | `https://api.deepseek.com` | `deepseek-chat` |
| 4 | `qwen` | `DASHSCOPE_API_KEY` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen-plus` |
| 5 | `ark` | `ARK_API_KEY` | `https://ark.cn-beijing.volces.com/api/v3` | none: set `ARK_MODEL`, use `ark/<id>`, or an alias |

`deepseek`, `qwen` and `ark` are presets of the OpenAI-compatible provider. Anything else
that speaks the Chat Completions API (Ollama, vLLM, LiteLLM, Bifrost, an internal gateway)
works through `openai` with a `base_url`, ideally wrapped in an alias.

### Precedence

| Setting | Highest priority → lowest |
|---|---|
| Model name | `-m` → `$WUZUO_MODEL` → `llm.model` → prompt (terminal) or default |
| Provider | the model name's own provider (alias or `provider/…`) → `$WUZUO_PROVIDER` → `llm.provider` → first provider whose key is set |
| Endpoint | alias `base_url` → `llm.base_url` → `ANTHROPIC_BASE_URL` / `OPENAI_BASE_URL` → provider default |
| API key | alias `api_key_env` → `llm.api_key_env` → provider's key variables |

`llm.base_url` and `llm.api_key_env` apply only to `llm.provider` (or to any provider when
`llm.provider` is empty). They are not carried over when `-m` picks a different provider.

## Targets

Each target is one server WuZuo may examine. Select targets with `-t` (repeatable or
comma-separated). Without `-t`, every configured target is available.

```yaml
targets:
  - name: prod-orders
    type: mysql
    description: primary of the orders cluster        # shown to the model
    dsn_env: WUZUO_PROD_ORDERS_DSN
  - name: prod-orders-replica
    type: mysql
    description: read replica of prod-orders
    dsn_env: WUZUO_PROD_ORDERS_REPLICA_DSN
```

```bash
wuzuo ask "replica is lagging" -t prod-orders,prod-orders-replica
```

- `name` must be unique. The model uses it to address the target.
- `type` is `mysql` for now.
- `description` helps the model understand the server's role. Use it.
- `dsn_env` (recommended) names the environment variable that holds the connection string.
  `dsn` puts it in the file directly; use that only for local experiments, since it writes
  the password to disk.

### MySQL connection strings

The format is the [Go MySQL driver DSN](https://github.com/go-sql-driver/mysql#dsn-data-source-name):

```text
user:password@tcp(host:3306)/
user:password@tcp(host:3306)/?tls=true            # TLS with system CAs
user:password@tcp(host:3306)/?tls=skip-verify     # TLS without verification (not recommended)
user:password@unix(/var/run/mysqld/mysqld.sock)/
```

You do not need to add session settings. WuZuo sets `transaction_read_only=ON`,
`max_execution_time` and `lock_wait_timeout` on every connection it opens. Create the
account with [`docs/mysql-privileges.sql`](mysql-privileges.sql).

## Safety settings

```yaml
safety:
  redact: true
  query_timeout: 10s
  max_evidence_chars: 12000
  allow_explain: false
  allow_raw_sql: false
```

- **`redact`**: replaces literal values in SQL text with `?`, removes InnoDB row dumps,
  and strips credentials before anything reaches the model or the evidence locker. Leave
  it on.
- **`query_timeout`**: upper bound for each diagnostic query, also applied as
  `max_execution_time`.
- **`max_evidence_chars`**: how much of one evidence item the model sees in a tool
  result. The full content is still sealed on disk, and the model can page through it
  with `read_evidence`.
- **`allow_explain`** and **`allow_raw_sql`**: enable the two probes where the model
  writes SQL itself. Both run read-only and are off by default; see the safety model in
  the [README](../README.md#safety-model).

| Switch | Probe | What it allows |
|---|---|---|
| `allow_explain` | `mysql_explain` | `EXPLAIN` of a SELECT. The statement is never executed and no rows are returned. |
| `allow_raw_sql` | `mysql_query` | One guarded read-only query, capped at 200 rows. Use it for diagnostic views no dedicated probe covers. |

## Reference: all fields

| Field | Type | Default | Description |
|---|---|---|---|
| `llm.provider` | string | detected | `anthropic`, `openai`, `deepseek`, `qwen` or `ark` |
| `llm.model` | string | asks, or default | Short name, alias, `provider/model-id`, or model ID |
| `llm.base_url` | string | provider default | API endpoint, for gateways and self-hosted models |
| `llm.api_key_env` | string | provider's variables | Name of the environment variable holding the key |
| `llm.effort` | string | `high` | Reasoning depth, Anthropic only: `low`, `medium`, `high`, `xhigh`, `max` |
| `llm.max_steps` | int | `40` | Maximum model turns per investigation |
| `llm.refusal_fallback` | bool | `true` | Anthropic API only: when the model declines a request, the API retries it on a fallback model instead of failing the case. Not applied through custom `base_url` gateways. |
| `models.<name>.provider` | string | required | Provider of the alias |
| `models.<name>.model` | string | required | Model ID the alias stands for |
| `models.<name>.base_url` | string | provider default | Endpoint for this alias |
| `models.<name>.api_key_env` | string | provider's variables | Key variable for this alias |
| `targets[].name` | string | required | Unique target name |
| `targets[].type` | string | required | `mysql` |
| `targets[].description` | string | | Role of the server, shown to the model |
| `targets[].dsn_env` | string | one of these two | Environment variable holding the DSN |
| `targets[].dsn` | string | one of these two | DSN written inline (discouraged) |
| `safety.redact` | bool | `true` | Redact literals, row dumps and secrets |
| `safety.query_timeout` | duration | `10s` | Timeout per diagnostic query (`5s`, `1m`…) |
| `safety.max_evidence_chars` | int | `12000` | Characters of each evidence item shown to the model |
| `safety.allow_explain` | bool | `false` | Enable `mysql_explain` |
| `safety.allow_raw_sql` | bool | `false` | Enable `mysql_query` |
| `cases_dir` | path | `$WUZUO_HOME/cases` | Where cases, evidence and reports are stored; `~` is expanded |

## Reference: environment variables

| Variable | Purpose |
|---|---|
| `WUZUO_CONFIG` | Config file path |
| `WUZUO_HOME` | Base directory for the default config file and cases (default `~/.wuzuo`) |
| `WUZUO_PROVIDER` | Override `llm.provider` |
| `WUZUO_MODEL` | Override `llm.model` (any model name; `-m` wins over it) |
| `WUZUO_MYSQL_DSN` | Zero-config MySQL target named `mysql` |
| `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL` | Anthropic provider |
| `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL` | OpenAI-compatible provider |
| `DEEPSEEK_API_KEY` | DeepSeek preset |
| `DASHSCOPE_API_KEY` | Qwen preset |
| `ARK_API_KEY`, `ARK_MODEL` | Volcengine Ark preset |

## Checking your setup

```bash
wuzuo models                 # model names and which keys are set
wuzuo toolset check          # model, connectivity, privileges, server features
wuzuo toolset list           # probes available with the current config
wuzuo toolset run mysql_overview   # run one probe by hand
```

`toolset check` exits non-zero if any check fails, so you can also use it in scripts.

## Troubleshooting

| Message | Fix |
|---|---|
| `no LLM provider configured` | Set one of the key variables above, or `llm.provider` |
| `provider openai: no model selected` | Use `-m openai/<id>`, set `OPENAI_MODEL` (`ARK_MODEL` for ark), or define an alias |
| `provider deepseek: set DEEPSEEK_API_KEY` | The chosen name belongs to a provider whose key is not set; see `wuzuo models` |
| `api_key_env: environment variable X is not set` | Export the variable named in the alias or in `llm.api_key_env` |
| `target "x": environment variable Y is not set` | Export the DSN variable named in `dsn_env` |
| `read config: … no such file` | A path given with `-c` or `WUZUO_CONFIG` does not exist |
| `unknown target "x"` | The `-t` name does not match any `targets[].name` |
| `mysql_explain is disabled` | Set `safety.allow_explain: true` |
| `✗ … PROCESS privilege` | Apply [`docs/mysql-privileges.sql`](mysql-privileges.sql) |
| `✗ … least privilege — account has ALL PRIVILEGES` | Use a dedicated read-only account, not root |
