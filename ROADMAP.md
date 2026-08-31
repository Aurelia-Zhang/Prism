# Prism Roadmap

Prism is rebuilt through seven reviewable vertical MRs. Status values follow `AGENTS.md`.
T1 and T2 are sequential; after their contracts stabilize, C1, O1, and the independent parts of
E1 may be developed in parallel.

| ID | Status | MR boundary | Reproducible acceptance evidence |
|---|---|---|---|
| B0 | verified | Repository contract, `prism-mr` skill, Roadmap, resume evidence ledger, PR template, and truth-only README | Skill validation, repository build/test, reviewed Git diff, and [PR #1](https://github.com/Aurelia-Zhang/Prism/pull/1) |
| T1 | planned | In-memory Trace model, Provider-neutral Agent Loop, JSON Schema tool registry, validation feedback, ordered parallel tool execution, cancellation and loop limits | Unit/integration tests with a fake Provider plus a real in-memory Trace from the tested runtime path |
| T2 | planned | OpenAI Responses and Anthropic Messages adapters, streaming aggregation, unified errors and bounded retry, MCP stdio discovery/execution | Adapter fixtures plus separately recorded real OpenAI, Anthropic, and MCP smoke tests; unavailable live tests remain `partial` |
| C1 | planned | Stable context prefix, structured compression, large-output persistence/retrieval, SQLite session/project/long-term memory, BM25 and vector-cosine hybrid recall | Long-context and retrieval scenarios, persisted artifacts, Token/context-size comparison, known scale limits |
| O1 | planned | Persistent task state machine, Sub-Agent worktree isolation, progress/cancel/retry, structured result return, fan-out arbitration, complexity/budget routing | Multi-worker scenario including failure/retry, state transitions, worktree evidence, route reasons and per-agent cost/latency Trace data |
| E1 | planned | OpenTelemetry export, SQLite Trace persistence, timeline replay, scenario Eval runner with rule assertions and optional LLM-as-Judge | Export/replay tests and a reproducible Eval run generated from real Prism execution paths |
| E2 | planned | Version/model comparisons, final README, completed resume evidence, and interview deep-dive notes | Checked-in report with real sample size/method, reproducible commands, Commit/PR links, limitations, and final claim audit |

## Scope guardrails

- The target is a working, measurable personal Agent Harness, not production infrastructure.
- HA, distributed consistency, exhaustive protocol compatibility, broad security hardening, and
  large-scale load testing are out of scope unless later evidence shows direct interview value.
- Reference source is consulted selectively when a Roadmap item needs a concrete design decision;
  copying its architecture wholesale is not a deliverable.
