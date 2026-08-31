# O1 Local Orchestration Evidence

This report records the scenario produced by
`go test ./internal/orchestration -run 'TestTask|TestWorktree|TestCommandWorker|TestManagerCancellation|TestRuleRouter|TestFanOut' -count=1 -v`.
The test uses a temporary Git repository and the current Go test executable as the Worker command.
The worktrees are intentionally temporary and are not merged or cleaned by the Manager.

## Scenario

The parent task `parent-demo` started with status `queued`, transitioned to `running`, launched
the `stable` and `flaky` strategy children concurrently, and finished `succeeded` after
deterministic arbitration. The stable child succeeded on attempt 1. The flaky child was
`queued -> running -> failed` on attempt 1, then `retry -> queued -> running -> succeeded` on
attempt 2. The parent selected the stable child because both successful candidates scored 101 and
the lexical task ID tie-break selected `task-1788189934305725000-8`.

## Worktree Evidence

The base ref was explicitly `main`; each branch was created with `git worktree add -b` and remained
independent from the Prism checkout.

| Strategy | Attempt | Branch | HEAD | Changed files | Diff summary |
|---|---:|---|---|---|---|
| stable | 1 | `prism/task-1788189934305725000-8-1` | `6571cb1a5c169eb73f5c98811e0389a043163a0c` | `stable.txt` | 1 changed file; untracked changes are included |
| flaky | 1 | `prism/task-1788189934306033000-10-1` | `6571cb1a5c169eb73f5c98811e0389a043163a0c` | `flaky.txt` | 1 changed file; untracked changes are included |
| flaky | 2 | `prism/task-1788189934306033000-10-2` | `6571cb1a5c169eb73f5c98811e0389a043163a0c` | `flaky.txt` | 1 changed file; untracked changes are included |

## Worker And Routing

| Strategy | Attempt | Status | Duration | Fixture usage | Route reason |
|---|---:|---|---:|---|---|
| stable | 1 | succeeded | 90.323ms | input 11, output 7 | `tool_required=true and complexity=5 fit the balanced profile` |
| flaky | 1 | failed | 90.304ms | input 5, output 3 | `tool_required=true and complexity=5 fit the balanced profile` |
| flaky | 2 | succeeded | 78.539ms | input 11, output 7 | `retry history=1 requires a stronger profile` |

The arbiter used `succeeded => 100 + changed_files`, so stable and flaky attempt 2 each scored
101. Its reason was `selected the highest deterministic score; ties use task ID` and the winner
was `task-1788189934305725000-8` (`stable`). No currency cost was calculated or inferred.

The Trace contained `model.route`, `task.spawn`, `task.wait`, `task.retry`, and `task.arbitrate`
spans with task ID, attempt, strategy, profile, route reason, status, usage, and error-code
attributes where applicable.

The Worker is a local fixture implemented by `TestWorkerFixtureProcess` in
`internal/orchestration/orchestration_test.go`. It validates the real JSON stdin/stdout and
context-terminated subprocess path, but it is not an OpenCode or Codex live compatibility test.
