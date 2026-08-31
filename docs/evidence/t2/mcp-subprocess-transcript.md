# MCP Local Subprocess Transcript

Evidence class: a local fixture running as a real child process with stdin/stdout JSON-RPC lines.
It is not evidence of compatibility with a third-party MCP server.

```text
client -> initialize(id=1, protocolVersion=2024-11-05)
server -> result(id=1, capabilities={}, serverInfo=fixture)
client -> notifications/initialized
client -> tools/list(id=2)
server -> result(id=2, tools=[echo(inputSchema=object)])
client -> tools/call(id=3, name=echo, arguments={"value":"ok"})
server -> result(id=3, content=[text("called echo with {\"value\":\"ok\"}")], isError=false)
client -> tools/call(id=4, name=wait, arguments={})
client -> notifications/cancelled(requestId=4)
server -> result(id=4, content=[text("cancelled")])
server -> exit(stderr capped at configured limit)
client -> registry removes mcp.<server>.* and keeps local tools
```

Covered by `internal/mcp` tests: initialization, discovery, call results, JSON-RPC errors,
context cancellation, bounded stderr, process exit, and per-server tool removal. The external
smoke is opt-in through `PRISM_MCP_EXTERNAL_COMMAND` and was not run in the default environment.
