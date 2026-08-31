# C1 Evidence Report

## Method

The scenarios below run against the current repository tests and use a deterministic
summarizer and deterministic embedding fixture. They demonstrate runtime behavior and
reproducibility, not live DeepSeek summary or embedding quality. C1 therefore remains
`partial`.

## Context Compaction

`internal/context/TestCompactKeepsStructuredSummaryRecentRoundsAndToolPair` uses a
three-round conversation, retains the newest two rounds, and treats the assistant tool
call plus tool result as one retained round.

| Measure | Before | After |
|---|---:|---:|
| Estimated tokens | 362 | 176 |
| Messages | 9 | 8 |

The compacted prefix contains `completed`, `pending`, `key_files`, `decisions`, and
`constraints`. The token count is Prism's stable four-runes-per-token estimate, not a
DeepSeek tokenizer count.

## Large Output

`internal/agent/TestRunOptionalC1RuntimeRecallsCompactsAndPersistsOutput` returns 80
`x` bytes from a tool. The complete output is written under `t.TempDir()` and only this
provider-facing JSON metadata summary is fed into the next call:

| Measure | Bytes |
|---|---:|
| Original tool output | 80 |
| Rehydrated metadata summary | 165 |

`internal/output/TestPersistFetchAndHash` validates the SHA-256 metadata and a byte range
fetch. The `fetch_output` tool also returns the requested range with its original size.

## Retrieval

The artificial relevance set contains one query, `database`, with two relevant entries
(`database indexing` and `sqlite database`) and one non-relevant entry (`garden notes`).
At `k=1`, all observed top results are relevant:

| Mode | Top-1 result | Relevant | Recall@1 |
|---|---|---|---:|
| BM25 | database indexing | yes | 1.0 (1/1) |
| Vector cosine | sqlite database | yes | 1.0 (1/1) |
| Hybrid RRF | database indexing | yes | 1.0 (1/1) |

This is a small hand-written fixture, not a general retrieval benchmark. The SQLite
schema is version 1, FTS5 supplies BM25, float32 vectors supply cosine similarity, and
hybrid ranking uses reciprocal rank fusion with constant 60.

## Reproduction

```bash
go test ./internal/context ./internal/memory ./internal/output ./internal/agent
```

The full repository validation is reported with the C1 handoff. No live Provider,
DeepSeek summarizer, or embedding service was used for these measurements.
