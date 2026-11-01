# Contributing to WuZuo

Thanks for helping! Issues and pull requests are welcome in English or Chinese.

## Development setup

Requirements: Go (see `go.mod`), Docker, and optionally [golangci-lint](https://golangci-lint.run/) v2.

```bash
make build              # bin/wuzuo
make test               # unit tests (no database or LLM needed)
make lab-up             # disposable MySQL 8.4 on :13306
make test-integration   # probes against the lab
make lint
```

Unit tests never call a real LLM: the investigation loop is tested with scripted
providers and the adapters with recorded HTTP exchanges.

## Adding a probe

Probes live in `toolsets/<name>/` and implement `pkg/toolset.Tool`. A good probe:

- is read-only and documents the privileges it needs (update `docs/mysql-privileges.sql`);
- returns compact text (use `toolset.Table` / `toolset.KV`) with units and sampling
  windows spelled out. The model reads this output, and it is saved as evidence;
- passes statement text through `ts.sqlText` (literal redaction) and never selects
  application rows;
- states caveats in its description (for example "cumulative since startup");
- has an integration test, and ideally a lab scenario where it is needed to find the cause.

## Adding a toolset

Implement `pkg/toolset.Toolset`, add a target type in `internal/config`, wire it in
`internal/cli/toolsets.go`, and add a lab under `deploy/lab`.

## Commits and pull requests

- Use [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`,
  `docs:` …). Release notes are generated from them.
- Every Go file starts with `// SPDX-License-Identifier: Apache-2.0`.
- Keep pull requests focused, and include how you tested.

By contributing you agree that your contributions are licensed under the Apache License 2.0.

## Code of conduct

This project follows the [Contributor Covenant 2.1](https://www.contributor-covenant.org/version/2/1/code_of_conduct/).
Report unacceptable behavior to the maintainers through a private GitHub security advisory
or by email to the address on the maintainer's GitHub profile.
