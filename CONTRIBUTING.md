# Contributing to WuZuo

Thanks for helping! Issues and pull requests are welcome in English or Chinese.

## Proposing work

Planned work is tracked as issues with `priority/P0`–`priority/P3` and `area/*` labels.
Features labelled `needs-design` are discussed in their issue first: the design and its
open decisions are agreed there, the label is removed, and only then is the feature
implemented. Small fixes can go straight to a pull request.

## Development setup

Requirements: Go (see `go.mod`), Docker, and optionally [golangci-lint](https://golangci-lint.run/) v2.

```bash
make build              # bin/wuzuo
make test               # unit tests (no database or LLM needed)
make lab-up             # disposable MySQL 8.4 on :13306
make test-integration   # probes against the lab
make lab-stop           # pause the lab (keeps data); make lab-destroy removes it
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
`internal/cli/toolsets.go`, and add a lab under `lab`.

## Updating a built-in model

Model IDs appear in exactly one place in the code: `BuiltinModels` in
`internal/config/provider.go`. Provider defaults refer to it by short name, and
per-model behaviour (such as refusal fallback) is a field on its rows.

1. Run `wuzuo models fetch` with the provider's key to confirm the new model ID and
   that your key can use it.
2. Change the row: `Model`, and `RefusalFallback` if the new model's support differs.
3. Run `go test ./internal/config/`. `TestDocsListBuiltinModels` lists every line in
   the README and `docs/configuration*.md` that still shows the old ID; update them.
4. Use a `feat:` commit that names the short name and both IDs, so the release notes
   tell users what changed.

Tests elsewhere look IDs up from the table, so they do not need editing.

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
