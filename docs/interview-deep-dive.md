# Interview Deep Dive

面试深挖问答。每题只写能口头讲清楚的核心答案，代码和证据链接用于备查。数字来自
[metrics/final-report.json](metrics/final-report.json)，全部为 fixture/本地验证。

## 1. 为什么做 Agent Harness，而不是普通聊天 Demo？

聊天 Demo 只覆盖“发消息、收回复”；Agent 的核心难点在回复之外：工具调用循环、上下文增长、
多任务状态、失败恢复和可解释性。我把这些做成一个可测的最小闭环——loop、tool、trace、
context、orchestration、observability、eval 七层，每层都有测试和 checked-in 证据。
个人项目的取舍是：不做基础设施（HA/分布式），做一条能端到端讲清楚的 core path。
证据：[ROADMAP.md](../ROADMAP.md) scope guardrails、[README](../README.md) 架构图。

## 2. Provider-neutral Agent Loop 怎么设计？

`internal/provider/provider.go` 定义唯一能力接口 `Complete(ctx, messages, tools)`，
加上 Message/OutputItem/ToolCall/ToolResult/Usage/StopReason/结构化 Error 这些中立契约。
Loop（`internal/agent/agent.go`）只依赖契约：调 provider → 校验输出一致性（`validateResponse`
检查 stop_reason 与 tool_call 互相矛盾的情况）→ 并发执行工具 → 按序回灌 → 下一轮，
直到 end_turn/cancel/fatal/max_rounds。换 Provider 不改 Loop；Fake Provider 就能测全部
运行时行为。测试：`TestRunStopsNormallyWithoutTools`、`TestRunProviderFatalError`、
`TestRunCancellationAndMaxRounds`。

## 3. Streaming 与 provider-specific continuation 怎么处理？

`StreamingProvider` 是可选接口，不实现就退回 `Complete`，Loop 不感知。SSE 事件先聚合为
有序 items，同时通过 `EventSink` 吐 provider-neutral 的增量事件（text/tool_arguments delta）。
Provider 专属的续传数据（OpenAI `reasoning.encrypted_content`、Anthropic thinking signature）
装进 `OpaqueItem`，运行时原样存储和回传、从不解释字段——协议细节被限制在 adapter 内部。
测试：`TestOpenAIResponsesAggregatesOrderedItemsAndContinuation`、
`TestAnthropicMessagesAggregatesThinkingToolAndUsage`（fixture，[t2 fixture-summary](evidence/t2/fixture-summary.md)）。

## 4. 工具系统：JSON Schema、自修正、并行、顺序回灌？

注册时校验 schema（`internal/tool/registry.go`）；调用前先验证参数。验证失败不当异常，
而是生成 `tool_arguments_schema_invalid` 的 ToolResult 回灌，模型下一轮自己改参数——
T1 trace 里 `{"value":9}` 被拒、下一轮 `{"value":"fixed"`} 成功就是这条路径。
同一轮的多个 tool.call 并发执行（每个 handler 独立 goroutine，测试用 channel 阻塞证明
真并发），但结果按 call 顺序写回 conversation，保证 provider 看到的顺序确定。
代码：`agent.go executeTools`；测试：`TestRunExecutesSameRoundToolsConcurrentlyAndKeepsOrder`、
`TestRunFeedsValidationFailureAndHandlerFailureBack`；artifact：[t1/trace.json](evidence/t1/trace.json)。

## 5. 为什么 Trace-first？持久化、回放、Eval 怎么串起来？

每个 run 天然产生 span 树（agent.run → model.call/tool.call → context.compact/
memory.recall/output.persist），usage 聚合到根 span。这棵树是所有下游的单一事实源：
`internal/observability` 把 Snapshot 原子写进 schema-v1 SQLite；`trace show` 只读库重建
时间线（绝不重跑 provider/tool，所以回放无副作用）；`ExportSnapshot` 把同一棵树映射到
官方 OTel SDK；`internal/eval` 的断言直接消费真实 Runner 的 Result + Trace。先有 Trace，
观测、回放、Eval 都是它的只读视图。测试：`TestTraceSQLiteRoundTrip`、
`TestReplayPreservesSequenceAndParentDepth`、`TestE1EvidenceArtifacts`。

## 6. Context compression、分层记忆、RRF、大输出管理？

- 压缩（`internal/context/compact.go`）：按 user 消息边界分轮，assistant tool_call 和它的
  tool result 永远同进退；旧轮替换成一条结构化 system 前缀（completed/pending/key_files/
  decisions/constraints），保留最近 N 轮。362→176 是这条路径的 fixture 数字。
- 记忆（`internal/memory/store.go`）：session/project/long-term 三个 scope 隔离；BM25 走
  SQLite FTS5，向量是 float32 余弦，hybrid 用 RRF（rank 倒数融合，常数 60）合并两路排名。
- 大输出（`internal/output` + `agent.go persistLargeOutput`）：超过阈值先落盘（SHA-256
  元数据），给模型的只有 output_id + original_bytes + 240 字节摘录的 JSON（4096→327），
  模型可用 `fetch_output` 按字节范围取回原文。
- 诚实声明：summarizer 和 embedding 都是 deterministic fixture，token 是 4 字符估算。

## 7. O1：task state machine、Sub-Agent、worktree、retry、routing、arbitration？

`internal/orchestration`：SQLite 持久化 task/attempt，状态转换集中在 `task.go` 校验
（queued/running/succeeded/failed/cancelled，非法转换直接报错），进程重启后可恢复。
Manager 对每个策略开独立 git worktree（`git worktree add -b`，互不污染主 checkout），
起 JSON stdin/stdout 子进程 Worker，收集结构化结果。失败按 attempt 重试；`RuleRouter`
按 complexity/tool_required/retry_history 选 profile 并给出人能读的理由（“retry
history=1 requires a stronger profile”）；fan-out 用确定性仲裁（`succeeded => 100 +
changed_files`，平局按 task ID），O1 报告里 101:101 平局选 stable 就是实例。
刻意不做自动 merge——仲裁结果给人看，合并留给 owner。测试：`TestTaskLifecycleRetryAndSQLiteReopen`、
`TestWorktreeIsolationAndManagerOwnedCleanup`、`TestFanOutRetryArbitrationAndTrace`。

## 8. OpenTelemetry 导出怎么脱敏？

原则：只有 span 层级 + allowlist 的非内容属性能出进程。`observability/otel.go` 只导
`prism.*` 白名单属性（span_id/status/usage/round 这类计量字段），不导 prompt 正文、
tool arguments/output、reasoning；不声称完整 GenAI semantic conventions。测试
`TestOTelMappingAndSensitiveAttributeFiltering` 直接断言敏感字段不出现在导出结果里；
artifact：[e1/otel.json](evidence/e1/otel.json)。

## 9. 指标的局限怎么说？

所有数字都是 fixture/local：C1 的 362→176、4096→327 是 deterministic fixture 单样本；
检索 Hit@1 只有 1 条手写 query，证明的是链路而非排序质量；O1 时延是单次本地 run 含
子进程启动；E1 是 5 个 fixture case，故意不算 P95、不做显著性、不做模型对比——样本量
撑不起这些结论。没有 baseline 的项就写“未提供可比 baseline”，不编对比数字。
详见 [metrics/final-report.md](metrics/final-report.md) 的 limitations。

## 10. 为什么没做 HA、Swarm、外部向量库、完整生产安全？

个人项目的价值在“能讲清楚的设计取舍”，不在堆基础设施。HA/分布式一致性对单人 Demo 是
负资产（没有真实故障场景去验证）；Swarm 式自由 Agent 通信不如 manager-worker +
确定性仲裁可解释可复现；外部向量库会把验证依赖拽到付费服务上，而 RRF+FTS5+本地向量
足够演示混合召回的机制；生产安全系统的水更深（权限、审计、合规），我只做了 OTel 导出
的内容白名单这一个真实且可测的点。这些边界写进了 [ROADMAP.md](../ROADMAP.md) 的
scope guardrails，面试时直接引用。
