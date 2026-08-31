# Implementation Prompt: prism-T1-runtime

You are the implementation Agent for Prism Roadmap item T1. Keep this Session focused on T1
implementation and its fixes. Read `AGENTS.md`, `.agents/skills/prism-mr/SKILL.md`, `ROADMAP.md`,
and `docs/resume-evidence.md` before editing.

## Background and current repository facts

Prism is a Go, trace-first Agent Harness for an autumn-recruiting portfolio. The repository has a
minimal `help/version` CLI and B0 collaboration/evidence files. It does not currently implement
Trace, a Provider runtime, or tool execution. Repositories under
`/Users/aurelia/career/refer-projects` and descriptions of the lost first version are references,
not Prism implementation evidence.

The project values a real, readable core path, reliable tests, measurable evidence, and interview
explainability. It is not production infrastructure.

## Single goal

Implement T1: an in-memory Trace model integrated with a Provider-neutral Agent Loop and a JSON
Schema-validated tool executor, including ordered parallel execution for same-round tool calls.

## In scope

- Provider-neutral contracts for messages, ordered model output items, tool calls/results, usage,
  stop reasons, and structured errors.
- An Agent Loop that calls a Provider, executes requested tools, feeds results back, and stops on
  normal completion, cancellation, fatal error, or a configured round limit.
- A concurrency-safe in-memory Trace recorder with explicit parent/child spans, running/ok/error
  lifecycle, structured errors, Token usage buckets, monotonic start/end ordering, first-wins span
  termination, and snapshots that cannot mutate recorder state.
- One run Trace with real `model.call` and `tool.call` child spans emitted by the actual loop path.
- Tool registration by unique name with JSON Schema input validation before invocation.
- Validation failures returned to the model as structured tool results without invoking the tool
  or aborting the loop.
- Same-round tool calls executed concurrently, with results fed back in original call order.
- Tests for lifecycle invariants, cancellation, round limits, validation feedback, concurrency,
  ordering, tool failure, and Trace parentage/status/usage.

## Out of scope

- OpenAI or Anthropic HTTP adapters, SSE parsing, credentials, and live Provider claims.
- Transport/rate-limit retry policies; these belong to T2. A model may still self-correct after a
  validation result in a later Agent Loop round.
- MCP, SQLite, context compression, memory, Sub-Agents, worktrees, OTel export, replay, and Eval.
- Built-in filesystem or shell tools, a production plugin system, HA, or broad security hardening.
- Performance claims without a defined method and reproducible sample.

## Existing contracts to preserve

- Do not break `cmd/prism` `help` and `version` behavior.
- Follow the evidence meanings and permissions in `AGENTS.md`.
- Keep Provider-specific concepts out of the loop and tool packages.
- Preserve model output order even if contracts use typed output items.
- Do not claim external Provider compatibility from the fake Provider used in tests.

If a new external dependency is needed for JSON Schema validation, choose one focused,
well-maintained Go module, record the reason in the final report, and avoid adding a framework.

## Required deliverables and behavior

- Small, cohesive Go packages for Trace, Provider contracts, tools, and Agent runtime.
- Public API comments where exported symbols need them; no speculative abstraction layer.
- Unit tests plus at least one integration-style test that runs the loop through a fake Provider,
  triggers valid and invalid tools, and asserts the resulting span tree and ordered feedback.
- A deterministic, human-readable Trace artifact generated from that tested runtime path under
  `testdata/` or `docs/evidence/`; document the command that regenerates it. Do not hand-author an
  artifact and present it as runtime output.
- Update T1 and only affected shared-Trace rows in `ROADMAP.md` and
  `docs/resume-evidence.md`. Use `partial` unless all evidence required by the row is present.
- Update README only if it can describe a command or behavior that is reproducible after this MR.

## Validation

Run and report the exact results of:

```bash
gofmt -w <changed-go-files>
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/prism
```

Also run the documented Trace-artifact regeneration command and verify the checked-in artifact
matches the generated output. If the environment cannot run a command, report that limitation;
do not substitute a claim.

## Evidence and state impact

- Required evidence: test names for loop/tool/Trace behavior and one runtime-generated Trace
  artifact. No live Provider metric is required in T1.
- `ROADMAP.md`: T1 may become `verified` only when its complete acceptance evidence passes on the
  current commit; otherwise mark `partial` and name the missing evidence.
- `docs/resume-evidence.md`: update the Agent Loop, tool execution, and shared Trace rows with code,
  tests, artifact paths, and limitations. OpenAI/Anthropic/MCP rows remain `planned`.

## Permissions

- You may inspect the repository, edit files within T1 scope, and run local validation commands.
- Commit, push, PR creation/modification, merge, tag, and publication are not authorized by this
  Prompt. Stop after a clean implementation handoff unless the user separately authorizes them.
- Preserve unrelated user changes and do not rewrite Git history.

## Final report format

1. Implemented scope and main design choices.
2. Changed files grouped by Trace, Provider, tool, runtime, tests, and evidence.
3. Every validation command with pass/fail outcome.
4. Trace artifact path and how it was generated.
5. Roadmap/resume-evidence state transitions.
6. External dependencies added and why.
7. Known limitations and explicitly unverified compatibility.
8. Git status and confirmation that no unauthorized commit/push/PR action occurred.
