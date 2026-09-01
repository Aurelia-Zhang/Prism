# Prism

Prism is a trace-first Agent Harness being rebuilt in Go for an autumn-recruiting portfolio.

## Current status

The repository currently contains:

- a minimal CLI with `help` and `version` behavior;
- a Provider-neutral internal Agent Loop with cancellation and round limits;
- a JSON Schema Tool Registry with same-round parallel execution and ordered result feedback;
- an in-memory Trace Recorder for `agent.run`, `model.call`, and `tool.call` spans;
- optional Provider streaming with OpenAI Responses and Anthropic Messages adapters, bounded retry,
  normalized errors, and opaque continuation items;
- an MCP stdio client for initialize, tool discovery, namespaced registration, and tool calls;
- optional C1 context compaction with structured summaries, SQLite session/project/long-term memory,
  BM25/vector hybrid recall, and large-output persistence with `fetch_output` retrieval;
- a local O1 orchestration package with persistent task/attempt state, rule-based model routing,
  isolated Git worktrees, JSON command Workers, retry, fan-out, and deterministic arbitration;
- schema-version-1 SQLite Trace persistence, stable timeline replay, and official OpenTelemetry
  SDK export with `prism.*` content-safe attributes;
- a versioned fixture Eval suite with real Agent Runner/O1 Manager execution and JSON/Markdown
  reports for steps, token buckets, success rate, and P50 latency;
- the B / T / C / O / E delivery Roadmap;
- collaboration, MR, and resume-evidence contracts.

The adapters, MCP client, O1 Worker path, and E1 Eval path are covered by readable local fixtures.
External OpenAI, Anthropic, third-party MCP, OpenCode/Codex Worker, and remote OTLP compatibility
is opt-in and remains `partial` until the corresponding live smoke is recorded. C1 uses
deterministic summarizer/embedding fixtures and remains `partial`; O1 deliberately does not merge
worktrees. E1 does not export API keys, complete prompts, tool arguments/output, or reasoning.
See [ROADMAP.md](ROADMAP.md) and [docs/resume-evidence.md](docs/resume-evidence.md) for evidence
state and limits.

## Run the current CLI

Prism requires Go 1.24 or newer.

```bash
go run ./cmd/prism version
go run ./cmd/prism help
go run ./cmd/prism trace show <db-path> <trace-id>
go run ./cmd/prism trace export <db-path> <trace-id>
go run ./cmd/prism eval run fixture --output docs/evidence/e1/eval-report.json
```

`trace show/export` read persisted spans only and never re-run a Provider or Tool. The explicit
`fixture` selector runs the five local E1 scenarios; the command also writes a Markdown report next
to the JSON fact source.

## Project boundaries

The goal is a real, measurable, Provider-neutral core that can be explained in interviews. Prism
does not target high availability, distributed consistency, exhaustive protocol compatibility,
or a production security platform.

## License

[MIT](LICENSE)
