# T2 Fixture Summary

Evidence class: readable local `httptest.Server` fixtures, not external compatibility evidence.

| Adapter | Fixture events | Aggregated output | Usage | Retry/error observations |
|---|---:|---|---|---|
| OpenAI Responses | 6 SSE events | 3 ordered items: function call, opaque reasoning, text; duplicate item IDs collapsed | input 11, output 7 | requests `reasoning.encrypted_content`; prior reasoning/tool output is remapped; `rate_limit` honors `Retry-After`; partial stream is not retried |
| Anthropic Messages | 11 SSE events, including `ping` and an unknown envelope | 2 ordered items: opaque thinking/signature, tool use with aggregated JSON arguments | input 5, output 4, cache-read 2, cache-creation 3 | prior thinking/tool result is remapped; `ping` and future envelopes are ignored; unknown content blocks remain `stream_protocol` |

The provider tests also exercise provider-specific default endpoints, arbitrary response write
chunks, request headers, model/tool mapping, provider-owned opaque filtering, `max_tokens` versus
Agent `max_rounds`, cancellation, and normalized authentication/permission/request/server error
mapping. No API key or model content is present in this artifact.
