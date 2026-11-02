<p align="center">
  <img src="docs/assets/banner.webp" width="360" alt="Yuanfang: 元芳 looking through a magnifier, asking 元芳，你怎么看？">
</p>

<p align="center">
  AI SRE forensics agent<br>
  <b>Every incident leaves evidence.</b>
</p>

<p align="center">
  开源的 AI 生产故障调查与根因分析 Agent。
</p>

<p align="center"><a href="README.md">English</a></p>

---

**元芳（Yuanfang）** 的名字来自狄仁杰断案故事里的李元芳。案情一有疑点，狄大人就会问一句“元芳，你怎么看？”。元芳排查数据库故障，回答的也是这个问题，只是不说“此事必有蹊跷”，而是拿证据说话：

- 先列出可能的原因，再用只读探针逐个取证，确认或排除。
- 每次探针的结果都存成编号证物（E1、E2……），SQL 里的值和凭据会被脱敏，并记录 SHA-256，事后被改动能查出来。
- 最后的诊断报告必须引用这些证物。元芳会逐条核对，引用了不存在的证物就打回重写；没有证据支撑的原因只能标为低置信度。
- 探针用最小权限账号连接，会话设置了 `transaction_read_only=ON` 和执行超时。元芳只给处置建议，不会执行。
- `yuanfang case replay` 可以基于已保存的证物重新分析，比如换一个模型，不需要再连生产库。
- 不只看结论，还要看证据：`make eval` 在靶场里制造真实故障，元芳不仅要找对根因，引用的证据还必须真能证明这个根因，否则不算通过。蒙对了也不算（[详见](#在靶场故障上自动评测)）。

> 状态：早期开发（v0.1）。目前支持 MySQL，下一个是 MongoDB。

## 快速开始

```bash
brew install yinx0004/tap/yuanfang                       # macOS 和 Linux
```

```bash
go install github.com/yinx0004/yuanfang/cmd/yuanfang@latest  # 已安装 Go 时
```

每个版本还会在 [Releases 页面](https://github.com/yinx0004/yuanfang/releases) 提供 Linux、macOS、Windows 的二进制，并发布镜像 `ghcr.io/yinx0004/yuanfang`。Homebrew 只更新正式版，不包含预发布版本。

**首次设置**：直接运行 `yuanfang`。第一次使用时，它会带你选择模型、输入 API key、添加 MySQL 目标（逐项填写主机、端口、账号、密码），然后测试连接。配置写入 `~/.yuanfang/config.yaml`；key 和 DSN 存进 `~/.yuanfang/credentials`（权限 0600），已经 export 的环境变量优先级更高。下面是这些设置的手动方式。

**选择模型**：元芳会从环境变量自动识别，设置以下任意一组即可，不需要写配置。

| 环境变量 | 模型服务 | 默认模型（[短名](docs/configuration.zh-CN.md#短名)） |
|---|---|---|
| `ANTHROPIC_API_KEY` | Anthropic Claude（原生 API） | `opus` |
| `OPENAI_API_KEY` + `OPENAI_MODEL`（+ `OPENAI_BASE_URL`） | OpenAI 或任意兼容接口：Ollama、vLLM、LiteLLM、Bifrost… | — |
| `DEEPSEEK_API_KEY` | DeepSeek | `deepseek` |
| `DASHSCOPE_API_KEY` | 通义千问 | `qwen` |
| `ARK_API_KEY` + `ARK_MODEL` | 豆包（火山方舟） | — |

用短名选择模型：`-m opus`、`-m sonnet`、`-m deepseek`、`-m qwen`，或者 `-m 服务商/模型ID`；`yuanfang models` 会列出所有名字。如果什么都没配置，`yuanfang ask` 会列出已设置 key 的模型让你选择，并询问是否保存。

**创建只读账号并开始调查**：

```bash
mysql < docs/mysql-privileges.sql          # 会生成随机密码并只显示一次，请妥善保存
export YUANFANG_MYSQL_DSN='yuanfang:...@tcp(10.0.0.5:3306)/'
yuanfang toolset check                        # 检查连通性、权限和服务端特性
yuanfang ask "订单库 10 点以后变慢了"
```

报告的语言跟随提问的语言：中文提问，就输出中文报告。如果有多个实例，先运行 `yuanfang init`，再把它们写进 `targets:`。凭据只从环境变量读取，配置文件里不会出现明文。

配置文件放在哪、全部字段和环境变量、模型的选择规则、常见报错，见 **[配置说明](docs/configuration.zh-CN.md)**。

## 在靶场里试一试

`lab/` 是给开发和演示用的本地测试环境，不是部署方式：Docker 里跑一个一次性的 MySQL 8.4，带种子数据 `shop` 库，再配上几个模拟真实故障的脚本。不要把这些脚本用在真实服务器上。

```bash
make lab-up && make build
eval "$(make -s lab-env)"
lab/scenarios/lock-wait.sh          # 还有 slow-query.sh、deadlock.sh、conn-pileup.sh
./bin/yuanfang ask "orders 表的更新全卡住了" -t lab
lab/scenarios/reset.sh                     # 结束正在模拟的故障
make lab-stop                              # 暂停靶场，保留数据
make lab-destroy                           # 彻底删除靶场
```

### 在靶场故障上自动评测

`make eval` 用靶场故障自动给元芳打分。每次运行会重启靶场 MySQL，制造故障，记下标准答案（锁等待场景记的是阻塞连接和等待连接的真实 ID），只把问题描述交给 `yuanfang ask`，再按规则给报告打分：根因找得对不对，引用的证据能不能证明，处置建议是否安全。每次结果是 `PASS`、`FAIL`，或者靶场本身出问题时记为 `ERROR`。评测会真实调用模型，会消耗 token。

评测对元芳的要求：

- **标准答案来自真实故障现场。**标准答案在制造故障时记录（锁等待场景记的是阻塞连接和等待连接的真实 ID），每次运行都不一样，不是事先写好的固定文本。
- **既查结论，也查证据。**报告引用的证据里必须真有它声称的事实，不管证据来自哪个探针。
- **靶场出问题不算元芳的错。**提问前先确认故障已经稳定，调查结束后再复查一次；如果是靶场出了问题，结果记为 `ERROR`，不记为 `FAIL`。
- **安全也打分。**建议了破坏性命令，或者 KILL 了错误的连接，即使诊断正确也判失败。
- **按规则判分，不用模型当裁判。**每个维度单独给结果，不合成一个总分，所以结果可以复现，每次失败都能看出具体漏了什么。

```bash
make eval EVAL_ARGS="-scenario lock-wait -runs 3"   # 结果在 eval/results/<时间>/
```

目前已有的场景：`lock-wait`。后续场景见 [#15](https://github.com/yinx0004/yuanfang/issues/15)，详细说明见 [eval/README.md](eval/README.md)。

## 命令

| 命令 | 说明 |
|---|---|
| `yuanfang ask <问题描述>` | 调查并输出诊断报告（`-m 模型`、`-t 目标`、`--json`、`-q`） |
| `yuanfang case list \| show <id>` | 浏览历史案件（支持 ID 前缀） |
| `yuanfang case show <id> --transcript` | 逐轮查看调查过程：模型看到了什么、思考摘要、调用了哪些探针、返回了什么（加 `--full` 显示完整内容） |
| `yuanfang case replay <id>` | 用封存的证物重新分析，例如换一个模型：`-m sonnet` |
| `yuanfang models` | 列出 `-m` 可用的模型短名，以及哪些 key 已设置 |
| `yuanfang models fetch` | 向各服务商查询当前可用的模型，提示更新的和已下线的 |
| `yuanfang toolset list \| check` | 列出探针、检查前置条件 |
| `yuanfang toolset run <探针> [json]` | 手动执行单个探针 |
| `yuanfang init` | 首次设置：选择模型、添加目标、生成 `~/.yuanfang/config.yaml` |

需要模型自己写 SQL 的两个探针默认关闭，可以在配置文件的 `safety:` 下分别打开：`allow_explain`（`mysql_explain`，查看 SELECT 的执行计划，不实际执行）和 `allow_raw_sql`（`mysql_query`，受控的只读查询）。

探针清单、安全模型和架构说明见 [English README](README.md#mysql-probes)。

## 路线图

- **[v0.1.0](https://github.com/yinx0004/yuanfang/milestone/1)**：MySQL 工具集，并用靶场故障场景做评测
- **[v0.2.0](https://github.com/yinx0004/yuanfang/milestone/2)**：MongoDB 工具集，扩展 MySQL 的覆盖面
- 之后：Prometheus 等数据源、HTTP 服务、飞书 / Slack 机器人、对接告警自动调查

所有计划都记录在 [issues](https://github.com/yinx0004/yuanfang/issues) 里，按优先级（`priority/critical`、`high`、`medium`、`low`）和模块打标签。

## 参与贡献

见 [CONTRIBUTING.md](CONTRIBUTING.md)。安全问题请按 [SECURITY.md](SECURITY.md) 私下报告。

## 许可证

[Apache License 2.0](LICENSE)
