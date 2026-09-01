# Prism Final Metrics Report

- Fact source: [`final-report.json`](final-report.json). Every number in this file is copied from that JSON.
- Produced by the E2 final evidence session. No new benchmark was run; the numbers were already produced by checked-in tests and evidence artifacts.
- Evidence class: all numbers are **fixture/local**. They come from a scripted fake provider, deterministic summarizer and embedding fixtures, a local test-executable Worker, and an in-memory OpenTelemetry exporter. No live OpenAI, Anthropic, third-party MCP, OpenCode/Codex Worker, or remote OTLP run was recorded.

## T1 — Agent runtime (fixture)

From [`docs/evidence/t1/trace.json`](../evidence/t1/trace.json), regenerated and byte-compared by `internal/agent TestTraceArtifact`:

- 4 provider calls, 9 spans in trace `t1-span-1`; aggregated usage input 37 / output 11 / cache-read 1 / cache-write 1.
- Self-correction: round 1 rejected `{"value":9}` as `tool_arguments_schema_invalid` and fed the error back as a tool result; round 2 re-called with `{"value":"fixed"}` and succeeded.
- Parallel tools: round 3 called `alpha` and `beta` in the same round; handlers block on channels in the test, proving concurrent execution, and results feed back in call order.
- 未提供可比 baseline：T1 is the first runtime version.

## T2 — Providers and MCP (local fixtures, not live compatibility)

From [`docs/evidence/t2/fixture-summary.md`](../evidence/t2/fixture-summary.md) and [`mcp-subprocess-transcript.md`](../evidence/t2/mcp-subprocess-transcript.md):

| Fixture | In | Out | Usage |
|---|---:|---:|---|
| OpenAI Responses (httptest SSE) | 6 events | 3 ordered items | input 11, output 7 |
| Anthropic Messages (httptest SSE) | 11 events | 2 ordered items | input 5, output 4, cache-read 2, cache-write 3 |
| MCP stdio child process | 4 scenarios | lifecycle / cancellation / exit / rpc-error | — |

- Retry behavior verified as pass/fail tests: `rate_limit` honors `Retry-After`; a partial stream is not retried; streaming cancellation stops without retry.
- Live smoke: not run — no `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, or `PRISM_MCP_EXTERNAL_COMMAND` was provided.
- 未提供可比 baseline：these are protocol-coverage observations, not throughput or latency measurements.

## C1 — Context and memory (deterministic fixtures)

From [`docs/evidence/c1/report.md`](../evidence/c1/report.md). Token counts are Prism's stable 4-runes-per-token estimate, not a provider tokenizer.

| Measure | Before | After | Reduction |
|---|---:|---:|---:|
| Context compaction, estimated tokens (1 conversation) | 362 | 176 | 51.4% |
| Context compaction, messages | 9 | 8 | — |
| Large tool output, provider-facing bytes (1 tool result) | 4096 | 327 | 92.0% |

Retrieval on a hand-written relevance fixture (1 query, k=1):

| Mode | Top-1 | Hit@1 |
|---|---|---:|
| BM25 | database indexing | 1.0 |
| Vector cosine | sqlite database | 1.0 |
| Hybrid RRF | database indexing | 1.0 |

- Sample size is 1 query; this demonstrates wiring, not ranking quality. Compaction before/after on the same conversation is the only comparison. 未提供可比 baseline for retrieval quality.

## O1 — Orchestration (single recorded local run)

From [`docs/evidence/o1/report.md`](../evidence/o1/report.md). The Worker is the Go test executable as a fixture; worktrees are temporary and not merged.

- Scenario: 1 parent task, 2 strategies (stable, flaky), 3 attempts, 3 worktrees created from `main`.
- Retry: flaky `queued -> running -> failed` (attempt 1), then `retry -> queued -> running -> succeeded` (attempt 2).
- Routing reasons: initial `tool_required=true and complexity=5 fit the balanced profile`; retry `retry history=1 requires a stronger profile`.
- Arbitration rule `succeeded => 100 + changed_files`: stable 101 vs flaky attempt 2 101; lexical task-ID tie-break selected the stable child `task-1788189934305725000-8`.
- Durations (single local run, includes subprocess startup; not a latency benchmark): stable 90.323 ms, flaky attempt 1 90.304 ms, flaky attempt 2 78.539 ms.
- Usage: stable in 11/out 7; flaky attempt 1 in 5/out 3; flaky attempt 2 in 11/out 7.
- 未提供可比 baseline：no previous orchestration implementation and no live Worker comparison exists.

## E1 — Observability and eval (fixture suite `e1-fixture-v1`)

From [`docs/evidence/e1/eval-report.json`](../evidence/e1/eval-report.json), re-run and byte-compared by `internal/eval TestE1EvidenceArtifacts`:

| Case | Steps | Latency (ms) |
|---|---:|---:|
| text-completion | 1 | 3 |
| single-tool | 3 | 7 |
| validation-self-correction | 5 | 11 |
| context-compact | 2 | 5 |
| failure-retry-routing | 7 | 15 |

Metrics: 5 cases, 5 passed, success rate 1.00; average steps 3.60; input tokens 80; output tokens 27; cache read/write 0/0; P50 latency 7 ms. P95, significance testing, LLM-as-Judge, and model comparison are intentionally absent at 5 cases. 未提供可比 baseline.

## Claim states after the final audit

- `verified`: B0 contracts; T1 agent runtime/tool/trace; E1 fixture eval runner (fixture evidence only).
- `partial`: T2 external OpenAI/Anthropic/third-party MCP compatibility; C1 real summarizer and embedding quality; O1 OpenCode/Codex live Worker compatibility; E1 remote OTLP, live provider, and Judge.

## Reproduction

```bash
go test ./internal/agent -run TestTraceArtifact -count=1
go test ./internal/eval -run TestE1EvidenceArtifacts -count=1
go run ./cmd/prism eval run fixture --output docs/evidence/e1/eval-report.json
go test ./internal/context ./internal/memory ./internal/output ./internal/agent
go test ./internal/orchestration -run 'TestTask|TestWorktree|TestCommandWorker|TestManagerCancellation|TestRuleRouter|TestFanOut' -count=1 -v
```
