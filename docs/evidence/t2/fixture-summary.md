# T2 Fixture Summary

Evidence class: readable local `httptest.Server` fixtures, not external compatibility evidence.

| Adapter | Fixture events | Aggregated output | Usage | Retry/error observations |
|---|---:|---|---|---|
| OpenAI Responses | 6 SSE events | 3 ordered items: function call, opaque reasoning, text; duplicate item IDs collapsed | input 11, output 7 | `rate_limit` honored `Retry-After: 1` for 3 attempts; partial stream made 1 attempt and returned `stream_protocol` |
| Anthropic Messages | 9 SSE events | 2 ordered items: opaque thinking/signature, tool use with aggregated JSON arguments | input 5, output 4, cache-read 2, cache-creation 3 | tool-use stop mapped to `tool_call`; unsupported blocks return `stream_protocol` |

The provider tests also exercise arbitrary response write chunks, request headers, model/tool
mapping, cancellation, and normalized authentication/permission/request/server error mapping in
the shared HTTP path. No API key or model content is present in this artifact.
