---
name: prism-mr
description: Implement or review one Prism Roadmap MR with explicit scope, reproducible tests, honest evidence states, and resume-claim traceability. Use for Prism feature MR planning, implementation, repair, or acceptance; not for unrelated repository questions.
---

# Prism MR

Deliver one B / T / C / O / E Roadmap item as a reviewable vertical change.

## Establish the boundary

Read `AGENTS.md`, the assigned item in `ROADMAP.md`, and `docs/resume-evidence.md`. Inspect the
current code and Git state before proposing changes. Treat reference projects and the lost first
version only as design input, never as current Prism evidence.

Before editing, state the single goal, in-scope behavior, out-of-scope behavior, existing contracts
that must remain compatible, validation plan, and Commit/push/PR permissions. Do not silently widen
the MR.

When preparing an implementation Prompt, include:

- background and current repository facts;
- one explicit goal, in scope, and out of scope;
- contracts to reuse and required files or observable behavior;
- exact test and validation commands;
- required real Trace, metric, or comparison evidence;
- Roadmap and resume-evidence state changes;
- Commit, push, and PR permissions;
- a final report format that includes known limitations.

## Implement and verify

Keep code proportional to an interview project: a real core path, reliable tests, and explainable
tradeoffs. Test doubles may verify internal behavior but cannot establish live Provider or MCP
compatibility. Do not add HA, distributed coordination, exhaustive protocol support, broad security
frameworks, or unrelated abstractions unless the assigned item requires them.

Update the Roadmap and evidence ledger in the same MR. Use `planned`, `partial`, and `verified`
according to `AGENTS.md`; never promote a claim because code merely compiles. README may mention
only behavior that is currently reproducible.

## Handoff

Report:

1. implemented scope and changed files;
2. exact validation commands and outcomes;
3. produced Trace, metric, or comparison artifacts;
4. Roadmap and resume-claim state transitions;
5. known limitations and unverified external compatibility;
6. Git status and whether commit, push, or PR actions were taken.
