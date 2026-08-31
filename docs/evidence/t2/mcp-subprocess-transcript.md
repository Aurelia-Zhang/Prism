# MCP Local Subprocess Scenarios

Evidence class: normalized per-scenario summary checked against `internal/mcp` tests. Each scenario
starts a separate real child process and therefore restarts request IDs at 1. This is not a
byte-for-byte capture and is not evidence of compatibility with a third-party MCP server.

```text
lifecycle session: initialize(id=1) -> initialized -> tools/list(id=2) -> tools/call(id=3, echo) -> result(id=3)
cancellation session: initialize(id=1) -> initialized -> tools/list(id=2) -> tools/call(id=3, wait) -> notifications/cancelled(requestId=3)
exit session: initialize(id=1) -> initialized -> tools/list(id=2) -> server exit -> bounded stderr -> remove only mcp.exit-fixture.*
rpc-error session: initialize(id=1) -> initialized -> tools/list(id=2) -> error(id=2, code=-32001)
```

`TestMCPScenarioArtifact` checks this file for drift. The subprocess tests cover initialization,
discovery, call results, JSON-RPC errors, cancellation, completed-request cleanup, bounded stderr,
process exit, and per-server tool removal. The external smoke is opt-in through
`PRISM_MCP_EXTERNAL_COMMAND` and was not run in the default environment.
