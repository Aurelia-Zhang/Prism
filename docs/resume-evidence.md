# Resume Evidence Ledger

This ledger is the source of truth for resume claims. States follow `AGENTS.md`. The lost first
version and repositories under `/Users/aurelia/career/refer-projects` are design references only.

Current repository fact: the repository contains a minimal `version/help` CLI and the verified T1
runtime described below. External Provider and MCP compatibility remain unverified.

| Claim | Roadmap | State | Code | Tests | Trace / metrics | Commit / PR | Known limits |
|---|---|---|---|---|---|---|---|
| Provider-neutral Agent Loop and unified message/tool/usage/stop contracts | T1 | verified | `internal/provider`, `internal/agent` | `internal/agent/TestRunStopsNormallyWithoutTools`, `TestRunFeedsSingleToolResultBack`, `TestRunProviderFatalError`, `TestRunCancellationAndMaxRounds` | `internal/agent/TestTraceArtifact`; [`docs/evidence/t1/trace.json`](evidence/t1/trace.json) | T1 commit/PR | Fake Provider verifies internal runtime behavior only; no external adapter |
| JSON Schema validation feeds tool errors back to the model; same-round tools execute concurrently and return in call order | T1 | verified | `internal/tool`, `internal/agent` | `internal/tool` registry tests; `internal/agent/TestRunExecutesSameRoundToolsConcurrentlyAndKeepsOrder`, `TestRunFeedsValidationFailureAndHandlerFailureBack` | [`docs/evidence/t1/trace.json`](evidence/t1/trace.json) | T1 commit/PR | In-memory registry; no MCP or built-in tools |
| OpenAI Responses and Anthropic Messages streaming, errors, and retry | T2 | planned | — | — | — | — | No adapters; no live compatibility evidence |
| MCP Client discovers and executes external stdio tools | T2 | planned | — | — | — | — | No MCP implementation or smoke test |
| Stable prompt/tool prefix and structured context compression preserve unfinished work and key file references | C1 | planned | — | — | — | — | No context assembler or Token comparison |
| Session/project/long-term SQLite memory uses BM25 and vector-cosine hybrid recall | C1 | planned | — | — | — | — | No schema, retrieval code, or quality measurement |
| Large tool outputs are summarized for the model, persisted, and retrievable by ID/range | C1 | planned | — | — | — | — | No output store or context-growth measurement |
| Orchestrator-Worker Sub-Agents run in isolated git worktrees and return structured results; fan-out supports arbitration | O1 | planned | — | — | — | — | No scheduler or worker runtime |
| Persistent task lifecycle supports progress, cancellation, retry, and model routing with reasons and per-Agent cost attribution | O1 | planned | — | — | — | — | No state store, routing policy, or cross-Agent Trace |
| One run produces model/tool/compression/routing spans, exports via OpenTelemetry, persists, and replays as a timeline | T1, E1 | partial | `internal/trace`, `internal/agent` | `internal/trace` tests and `internal/agent/TestTraceArtifact` | [`docs/evidence/t1/trace.json`](evidence/t1/trace.json) | T1 commit/PR | Only in-memory model/tool spans; no compression/routing, OTel export, persistence, or replay |
| Scenario Eval compares success rate, steps, Token cost, and latency using rule assertions and LLM-as-Judge where appropriate | E1, E2 | planned | — | — | — | — | No dataset, runner, judge, or report |

## Update rule

Every feature MR updates only the affected rows. Link stable repository paths and test names; add
Trace/report artifact paths and Commit/PR URLs when they exist. If live credentials or an external
system are unavailable, keep the external-compatibility part `partial` and say so explicitly.
