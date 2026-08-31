# Prism

Prism is a trace-first Agent Harness being rebuilt in Go for an autumn-recruiting portfolio.

## Current status

The repository currently contains:

- a minimal CLI with `help` and `version` behavior;
- the B / T / C / O / E delivery Roadmap;
- collaboration, MR, and resume-evidence contracts.

Trace recording, the Agent Loop, Provider adapters, tool execution, MCP, memory, Multi-Agent
orchestration, OpenTelemetry, replay, and evaluation are planned work. See [ROADMAP.md](ROADMAP.md)
and [docs/resume-evidence.md](docs/resume-evidence.md) for their evidence state.

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
