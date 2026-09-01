# Eval Report: e1-fixture-v1

Version: 1
Fixture: true

| Case | Passed | Status | Steps | Latency (ms) | Input | Output | Cache read | Cache write |
|---|---:|---|---:|---:|---:|---:|---:|---:|
| text-completion | true | succeeded | 1 | 3 | 10 | 4 | 0 | 0 |
| single-tool | true | succeeded | 3 | 7 | 20 | 6 | 0 | 0 |
| validation-self-correction | true | succeeded | 5 | 11 | 24 | 6 | 0 | 0 |
| context-compact | true | succeeded | 2 | 5 | 14 | 3 | 0 | 0 |
| failure-retry-routing | true | succeeded | 7 | 15 | 12 | 8 | 0 | 0 |

## Metrics

case_count: 5
success_rate: 1.00
average_steps: 3.60
input_tokens: 80
output_tokens: 27
cache_read_tokens: 0
cache_write_tokens: 0
p50_latency_ms: 7.00
