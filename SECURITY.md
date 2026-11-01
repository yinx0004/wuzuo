# Security Policy

WuZuo connects to production systems and sends diagnostic data to an LLM provider.

## Reporting a vulnerability

Please **do not open a public issue**. Report privately via
[GitHub Security Advisories](https://github.com/yinx0004/wuzuo/security/advisories/new).
We aim to acknowledge within 3 working days.

We are most interested in:

- any way for a probe or model output to modify a target system;
- bypasses of the read-only SQL guard (`toolsets/mysql/guard.go`);
- sensitive data (credentials, literal values, row data) reaching the model or the
  evidence locker despite redaction;
- tampering with sealed evidence going undetected.

## Where secrets are stored

API keys and DSNs come from environment variables or from `~/.wuzuo/credentials`, which
the setup wizard writes with mode 0600. `config.yaml` never contains secrets. Report
any path by which a secret ends up elsewhere: logs, case files, the config, or the model.

## What WuZuo keeps on disk

Each case directory (`~/.wuzuo/cases/<id>/`, files mode 0600) holds the sealed
evidence, the report, and `transcript.jsonl`: every message exchanged with the model,
including the system prompt, the redacted probe output it was shown, and the model's
replies and thinking summaries. The transcript contains exactly what was sent to and
received from the model, so it is as sensitive as that conversation and contains no
API keys or DSNs.

## What WuZuo sends to the model

The complaint you type, target names and descriptions, and redacted probe output. Probe
output contains server metadata such as hostnames, user names, schema and table names,
and normalized SQL. Review your LLM provider's data policy. For sensitive environments,
use a self-hosted model through an OpenAI-compatible endpoint.

## Supported versions

Security fixes go into the latest release.
