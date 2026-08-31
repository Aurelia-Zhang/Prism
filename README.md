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
- the B / T / C / O / E delivery Roadmap;
- collaboration, MR, and resume-evidence contracts.

The adapters and MCP client are covered by readable local fixtures. External OpenAI, Anthropic,
and third-party MCP compatibility is opt-in and remains `partial` until the corresponding live
smoke is recorded. C1 uses deterministic summarizer/embedding fixtures and remains `partial`;
Multi-Agent orchestration, OpenTelemetry, replay, and evaluation are not implemented. See
[ROADMAP.md](ROADMAP.md) and [docs/resume-evidence.md](docs/resume-evidence.md) for evidence state
and limits.

## Run the current CLI

Prism requires Go 1.24 or newer.

```bash
go run ./cmd/prism version
go run ./cmd/prism help
```

## Project boundaries

The goal is a real, measurable, Provider-neutral core that can be explained in interviews. Prism
does not target high availability, distributed consistency, exhaustive protocol compatibility,
or a production security platform.

## License

[MIT](LICENSE)
