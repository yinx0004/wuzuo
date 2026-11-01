# 配置说明

[English](configuration.md)

元芳**不需要配置文件也能运行**，只设置环境变量就够了。配置文件用于管理多个实例、固定使用某个模型，以及调整安全选项。

## 配置文件在哪里

按以下顺序查找，先找到哪个就用哪个：

1. 命令行参数 `--config` / `-c <路径>`
2. 环境变量 `$YUANFANG_CONFIG`
3. `$YUANFANG_HOME/config.yaml`，其中 `$YUANFANG_HOME` 默认是 `~/.yuanfang`

默认位置（第 3 种）没有文件时不会报错，直接使用环境变量。用第 1、2 种方式显式指定的文件如果不存在，会报错。

案件（证物和报告）默认存在 `$YUANFANG_HOME/cases`，可以用 `cases_dir` 修改。

### 首次设置

直接运行 `yuanfang`（不带参数）。如果还没有配置文件，它会询问是否现在设置；`yuanfang init` 则直接进入同样的步骤（加 `--force` 可覆盖已有文件）：

1. 选择默认模型。已设置 key 的模型排在前面；一个 key 都没有时，会列出全部内置模型，并注明各自需要哪个 key。
2. 粘贴该模型的 API key（如果对应的环境变量已经设置，会跳过这一步）。输入不回显，直接回车可以跳过。
3. 可选：添加一个 MySQL 目标。先填名字和给模型看的描述，再逐项填写连接信息：主机或 IP、端口（默认 3306）、账号（默认 `yuanfang`）、密码（不回显）。元芳用这几项拼出 DSN，尝试连接，并做和 `yuanfang toolset check` 一样的检查。主机一栏直接回车可以跳过连接设置。需要 TLS 或 Unix socket 时，事后在 credentials 文件里修改 DSN 即可（见 [MySQL 连接串](#mysql-连接串)）。
4. 写入配置文件，列出新建或修改了哪些文件，以及被跳过、还需要处理的项，例如：

```text
Files written:
  ~/.yuanfang/config.yaml   created: model opus, target orders-primary
  ~/.yuanfang/credentials   created: ANTHROPIC_API_KEY

Next steps:
  export YUANFANG_ORDERS_PRIMARY_DSN='user:password@tcp(host:3306)/'   # account: docs/mysql-privileges.sql
  (or run `yuanfang init --force` again to enter them; values go to ~/.yuanfang/credentials)
  yuanfang toolset check                  # verify model, connectivity and privileges
  yuanfang ask "describe what is wrong"
```

API key 和 DSN 不会写进 `config.yaml`，而是存进 credentials 文件（见[密钥](#密钥)）。不在终端里运行时，`yuanfang init` 不会询问任何内容，只写入带注释的模板，并使用默认模型。

### 密钥

`config.yaml` 只存放配置，可以放心分享。密钥来自环境变量，或者来自设置向导创建的 **`$YUANFANG_HOME/credentials`**（默认 `~/.yuanfang/credentials`）：

```text
# Yuanfang secrets: API keys and database DSNs, one KEY=VALUE per line.
ANTHROPIC_API_KEY=sk-ant-...
YUANFANG_ORDERS_PRIMARY_DSN=yuanfang:...@tcp(10.0.0.5:3306)/
```

- 启动时，元芳会把每一行导出为环境变量；如果同名环境变量已经设置，则跳过该行。**已经 export 的环境变量优先级始终高于这个文件。**
- 再次运行设置（`yuanfang init --force`）时，会显示文件里已经保存了哪些值，替换每一项之前都会先询问。
- 文件创建时权限为 0600；如果其他用户可读，元芳会给出警告。
- 可以手动编辑：每行可以以 `export ` 开头，`#` 开头的是注释。
- 要删除某个密钥，删掉对应那一行即可。

## 只用环境变量

```bash
export ANTHROPIC_API_KEY=...                              # 或其他模型服务，见下文
export YUANFANG_MYSQL_DSN='yuanfang:<密码>@tcp(10.0.0.5:3306)/'
yuanfang toolset check
yuanfang ask "订单库 10 点以后变慢了"
```

设置了 `YUANFANG_MYSQL_DSN`、而配置文件里没有定义 MySQL 目标时，会自动生成一个名为 `mysql` 的目标。

## 选择模型

### 短名

不需要记完整的模型 ID。在 `-m`、`llm.model` 或 `$YUANFANG_MODEL` 里写短名即可：

```bash
yuanfang ask -m sonnet "订单库变慢了"
yuanfang case replay 20261101-0556 -m deepseek     # 换一个模型复验
yuanfang models                                    # 列出所有名字，以及哪些 key 已经设置
```

内置短名（每个短名同时确定服务商和模型）。这张表由测试检查，保证和代码一致：

| 短名 | 服务商 | 模型 |
|---|---|---|
| `opus` | anthropic | `claude-opus-5-5` |
| `sonnet` | anthropic | `claude-sonnet-5-5` |
| `haiku` | anthropic | `claude-haiku-5-5` |
| `fable` | anthropic | `claude-fable-5-1` |
| `deepseek` | deepseek | `deepseek-chat` |
| `deepseek-r` | deepseek | `deepseek-reasoner` |
| `qwen` | qwen | `qwen-plus` |
| `qwen-max` | qwen | `qwen-max` |

内置短名在每个 元芳版本里是固定的，指向发版时的最新一代模型。每个案件都会记录实际使用的完整模型 ID。

### 检查有没有更新的模型

```bash
yuanfang models fetch          # 每家默认显示最新的 20 个；加 --all 显示全部
```

`fetch` 用你自己的 key，向每家已设置 key 的服务商（以及每个带自己 `base_url` 的别名）查询当前可用的模型。它会标出短名指向的模型，提示同一系列里更新的模型，并报告短名指向的模型已经不在列表里的情况：

```text
anthropic (ANTHROPIC_API_KEY)
  claude-opus-6      2026-12-01  newer than "opus" (claude-opus-5-5)
  claude-opus-5-5    2026-09-10  ← opus
  ...
  ! "fable" → claude-fable-5-1 is not in this list (retired, renamed, or not enabled for this key)
  To try it:   yuanfang ask -m anthropic/claude-opus-6 "..."
  To switch "opus" to it, add to the config:
    models: { opus: { provider: anthropic, model: claude-opus-6 } }
```

它不会修改任何东西，换不换模型由你决定。元芳不会自己去检查新模型，平时运行不会多出任何网络请求。如果配置的模型已经下线，`yuanfang ask` 会报 `model "…" is not offered by …` 并提示运行 `fetch`。

模型名还可以是：

- **自定义别名**：在配置文件的 `models:` 里定义（见下文），同名时优先于内置短名；
- **`服务商/模型ID`**：可以用服务商提供的任何模型，例如 `-m openai/gpt-5`、`-m ark/ep-20261101-xxxxx`；
- **只写服务商名**：例如 `-m ark`，使用该服务商的模型变量（`ARK_MODEL`、`OPENAI_MODEL`）或默认模型；
- **原始模型 ID**：发给 `llm.provider` 指定（或自动识别）的服务商，例如 Ollama 的 `qwen3:32b`。

### 自定义别名

给很长或者账号专属的 ID 起个短名：

```yaml
models:
  doubao: { provider: ark, model: ep-20261101-xxxxx }
  local:  { provider: openai, model: qwen3:32b, base_url: http://localhost:11434/v1, api_key_env: OLLAMA_API_KEY }
  gw:     { provider: openai, model: gpt-5, base_url: https://llm-gateway.internal.example.com/v1, api_key_env: GATEWAY_TOKEN }

llm:
  model: doubao
```

每个别名必须有 `provider` 和 `model`，`base_url` 和 `api_key_env` 可选。别名里不能有空格或 `/`。

### 没有配置模型时

如果 `-m`、`$YUANFANG_MODEL`、`$YUANFANG_PROVIDER`、`llm.model`、`llm.provider` 都没有设置，`yuanfang ask` 和 `yuanfang case replay` 会请你选择：

```text
No model is configured yet.
Choose a model for Yuanfang:
  1)  opus    anthropic/claude-opus-5-5    Claude Opus, strongest default     (default)
  2)  sonnet  anthropic/claude-sonnet-5-5  Claude Sonnet, faster and cheaper
  3)  haiku   anthropic/claude-haiku-5-5   Claude Haiku, fastest
  4)  fable   anthropic/claude-fable-5-1   Claude Fable, most capable
Also supported once their key is set:
     qwen, qwen-max — set DASHSCOPE_API_KEY
     deepseek, deepseek-r — set DEEPSEEK_API_KEY
Model [1]: 2
Save "sonnet" as the default model in /home/you/.yuanfang/config.yaml? [Y/n]
```

- 只列出 key 已经设置的模型。可以输入序号、名字或 `服务商/模型ID`，直接回车选默认项。
- 默认项是：按下表顺序，第一个设置了 key 的服务商的默认模型。
- 选择保存时，会把 `llm.model` 写入配置文件；文件不存在就按模板新建，已有文件里的其他配置和注释都会保留。
- 不在终端里运行时（CI、定时任务、标准输入被管道占用），不会弹出选择，直接使用默认项并打印一行提示。
- 一个 API key 都没有设置时，会在创建案件之前停下，列出每个环境变量分别对应哪些模型。

`yuanfang init` 新建配置文件时也会让你做同样的选择。

### 服务商和 key

| 顺序 | 服务商 | key 变量 | 接口地址 | 默认模型（短名） |
|---|---|---|---|---|
| 1 | `anthropic` | `ANTHROPIC_API_KEY` 或 `ANTHROPIC_AUTH_TOKEN` | 官方 API，或 `ANTHROPIC_BASE_URL` | `opus` |
| 2 | `openai` | `OPENAI_API_KEY` | OpenAI，或 `OPENAI_BASE_URL` | 无：设置 `OPENAI_MODEL` 或用 `openai/<ID>` |
| 3 | `deepseek` | `DEEPSEEK_API_KEY` | `https://api.deepseek.com` | `deepseek` |
| 4 | `qwen`（通义千问） | `DASHSCOPE_API_KEY` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen` |
| 5 | `ark`（豆包 / 火山方舟） | `ARK_API_KEY` | `https://ark.cn-beijing.volces.com/api/v3` | 无：设置 `ARK_MODEL`、用 `ark/<ID>` 或定义别名 |

`deepseek`、`qwen`、`ark` 都是 OpenAI 兼容接口的预设。其他兼容 Chat Completions 接口的服务（Ollama、vLLM、LiteLLM、Bifrost、公司内部网关）用 `openai` 加 `base_url` 即可接入，最好再定义一个别名。

### 优先级

| 项目 | 从高到低 |
|---|---|
| 模型名 | `-m` → `$YUANFANG_MODEL` → `llm.model` → 终端里询问，否则用默认项 |
| 服务商 | 模型名自带的服务商（别名或 `服务商/…`）→ `$YUANFANG_PROVIDER` → `llm.provider` → 第一个设置了 key 的服务商 |
| 接口地址 | 别名的 `base_url` → `llm.base_url` → `ANTHROPIC_BASE_URL` / `OPENAI_BASE_URL` → 服务商默认地址 |
| API key | 别名的 `api_key_env` → `llm.api_key_env` → 服务商自己的 key 变量 |

`llm.base_url` 和 `llm.api_key_env` 只对 `llm.provider` 生效（`llm.provider` 为空时对任何服务商生效）。用 `-m` 换成别的服务商时，不会沿用它们。

## 目标（targets）

每个目标对应一台可以排查的服务器。用 `-t` 选择目标（可以重复使用，也可以用逗号分隔）；不加 `-t` 时，所有已配置的目标都可用。

```yaml
targets:
  - name: prod-orders
    type: mysql
    description: 订单集群主库                # 会告诉模型
    dsn_env: YUANFANG_PROD_ORDERS_DSN
  - name: prod-orders-replica
    type: mysql
    description: prod-orders 的只读从库
    dsn_env: YUANFANG_PROD_ORDERS_REPLICA_DSN
```

```bash
yuanfang ask "从库延迟很大" -t prod-orders,prod-orders-replica
```

- `name` 不能重复，模型用它来指定要排查的目标。
- `type` 目前只支持 `mysql`。
- `description` 用来告诉模型这台机器的角色，建议填写。
- 推荐用 `dsn_env` 指定存放连接串的环境变量。`dsn` 会把连接串直接写进文件，密码也会落盘，只建议在本地测试时用。

### MySQL 连接串

格式是 [Go MySQL 驱动的 DSN](https://github.com/go-sql-driver/mysql#dsn-data-source-name)：

```text
user:password@tcp(host:3306)/
user:password@tcp(host:3306)/?tls=true            # 使用系统 CA 的 TLS
user:password@tcp(host:3306)/?tls=skip-verify     # 不校验证书的 TLS（不推荐）
user:password@unix(/var/run/mysqld/mysqld.sock)/
```

不需要自己加会话参数：元芳每次建立连接都会强制设置 `transaction_read_only=ON`、`max_execution_time` 和 `lock_wait_timeout`。账号请用 [`docs/mysql-privileges.sql`](mysql-privileges.sql) 创建。

## 安全选项

```yaml
safety:
  redact: true
  query_timeout: 10s
  max_evidence_chars: 12000
  allow_explain: false
  allow_raw_sql: false
```

- **`redact`**：把 SQL 里的值换成 `?`，删除 InnoDB 行数据转储，抹掉凭据，然后才交给模型或写入证物袋。建议保持开启。
- **`query_timeout`**：每条诊断查询的超时上限，同时作为 `max_execution_time` 生效。
- **`max_evidence_chars`**：每件证物在工具结果里给模型看的最大字符数。完整内容仍会落盘，模型可以用 `read_evidence` 翻看。
- **`allow_explain`** 和 **`allow_raw_sql`**：打开两个需要模型自己写 SQL 的探针。两者都只读，默认关闭，详见 [README 的安全模型](../README.md#safety-model)。

| 开关 | 探针 | 作用 |
|---|---|---|
| `allow_explain` | `mysql_explain` | 查看 SELECT 的执行计划，不实际执行，也不返回数据行 |
| `allow_raw_sql` | `mysql_query` | 执行一条受控的只读查询，最多 200 行；用于固定探针没覆盖到的诊断视图 |

## 字段参考

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `llm.provider` | 字符串 | 自动识别 | `anthropic`、`openai`、`deepseek`、`qwen`、`ark` |
| `llm.model` | 字符串 | 询问或用默认项 | 短名、别名、`服务商/模型ID` 或模型 ID |
| `llm.base_url` | 字符串 | 服务商默认 | 接口地址，用于网关和自部署模型 |
| `llm.api_key_env` | 字符串 | 服务商自己的变量 | 存放 key 的环境变量名 |
| `llm.effort` | 字符串 | `high` | 推理深度，只对 Anthropic 生效：`low`、`medium`、`high`、`xhigh`、`max` |
| `llm.max_steps` | 整数 | `40` | 每次调查最多让模型跑几轮 |
| `llm.refusal_fallback` | 布尔 | 不设置（自动） | 只对 Anthropic 官方 API 生效：模型拒答时由 API 换用备用模型重试，而不是让案件失败。不设置时：内置模型中已知支持该功能的自动打开，其他模型一律关闭；设为 `true` 可以对你确认支持的模型强制打开，设为 `false` 关闭。使用自定义 `base_url` 网关时不启用。 |
| `models.<名字>.provider` | 字符串 | 必填 | 别名对应的服务商 |
| `models.<名字>.model` | 字符串 | 必填 | 别名对应的模型 ID |
| `models.<名字>.base_url` | 字符串 | 服务商默认 | 该别名使用的接口地址 |
| `models.<名字>.api_key_env` | 字符串 | 服务商自己的变量 | 该别名使用的 key 变量 |
| `targets[].name` | 字符串 | 必填 | 目标名，不能重复 |
| `targets[].type` | 字符串 | 必填 | `mysql` |
| `targets[].description` | 字符串 | | 服务器角色，会告诉模型 |
| `targets[].dsn_env` | 字符串 | 二选一 | 存放连接串的环境变量 |
| `targets[].dsn` | 字符串 | 二选一 | 直接写连接串（不推荐） |
| `safety.redact` | 布尔 | `true` | 脱敏：值、行转储、凭据 |
| `safety.query_timeout` | 时长 | `10s` | 每条诊断查询的超时（`5s`、`1m`…） |
| `safety.max_evidence_chars` | 整数 | `12000` | 每件证物给模型看的字符数 |
| `safety.allow_explain` | 布尔 | `false` | 打开 `mysql_explain` |
| `safety.allow_raw_sql` | 布尔 | `false` | 打开 `mysql_query` |
| `cases_dir` | 路径 | `$YUANFANG_HOME/cases` | 案件、证物、报告的存放位置，支持 `~` |

## 环境变量参考

| 变量 | 用途 |
|---|---|
| `YUANFANG_CONFIG` | 配置文件路径 |
| `YUANFANG_HOME` | 默认配置文件、`credentials` 和案件的根目录（默认 `~/.yuanfang`） |
| `YUANFANG_PROVIDER` | 覆盖 `llm.provider` |
| `YUANFANG_MODEL` | 覆盖 `llm.model`（可以写任何模型名；`-m` 优先级更高） |
| `YUANFANG_MYSQL_DSN` | 免配置的 MySQL 目标，名为 `mysql` |
| `ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN`、`ANTHROPIC_BASE_URL` | Anthropic |
| `OPENAI_API_KEY`、`OPENAI_BASE_URL`、`OPENAI_MODEL` | OpenAI 兼容接口 |
| `DEEPSEEK_API_KEY` | DeepSeek |
| `DASHSCOPE_API_KEY` | 通义千问 |
| `ARK_API_KEY`、`ARK_MODEL` | 火山方舟 |

## 检查配置

```bash
yuanfang models                       # 列出模型名，以及哪些 key 已经设置
yuanfang toolset check                # 检查模型、连通性、权限、服务端特性
yuanfang toolset list                 # 当前配置下可用的探针
yuanfang toolset run mysql_overview   # 手动执行一个探针
```

`toolset check` 只要有一项失败就返回非零退出码，也可以用在脚本里。

## 常见报错

| 报错 | 解决办法 |
|---|---|
| `no LLM API key found` | 按提示导出其中一个 key 变量 |
| `model "opus" (anthropic) needs an API key` | 配置的模型对应的 key 没有设置；导出它，或用 `-m` 换一个模型 |
| `provider openai: no model selected` | 用 `-m openai/<ID>`、设置 `OPENAI_MODEL`（ark 用 `ARK_MODEL`），或定义别名 |
| `"…" is not offered by …` | 模型已下线、改名，或你的 key 没有开通。运行 `yuanfang models fetch` 查看 |
| `provider deepseek: set DEEPSEEK_API_KEY` | 选中的名字属于一个还没设置 key 的服务商，用 `yuanfang models` 查看 |
| `api_key_env: environment variable X is not set` | 导出别名或 `llm.api_key_env` 指定的变量 |
| `target "x": environment variable Y is not set` | 导出 `dsn_env` 指定的变量，或把它写进 credentials 文件 |
| `warning: … credentials is accessible by other users` | 执行 `chmod 600 ~/.yuanfang/credentials` |
| `read config: … no such file` | `-c` 或 `YUANFANG_CONFIG` 指定的文件不存在 |
| `unknown target "x"` | `-t` 写的名字和 `targets[].name` 对不上 |
| `mysql_explain is disabled` | 设置 `safety.allow_explain: true` |
| `✗ … PROCESS privilege` | 执行 [`docs/mysql-privileges.sql`](mysql-privileges.sql) |
| `✗ … least privilege — account has ALL PRIVILEGES` | 换成专用的只读账号，不要用 root |
