# Resume Bullets

可直接用于秋招简历的措辞。每条都对应 [resume-evidence.md](resume-evidence.md) 的行和
[metrics/final-report.json](metrics/final-report.json) 的数字。状态分三类：**可以直接说**、
**需要加“基于 fixture/本地验证”限定**、**当前不能说**。

## 可以直接说（内部行为有实现 + 测试 + artifact 证据）

**1. 设计并实现 Provider 中立的 Agent 运行时（Go）**

> 设计并实现 Provider 中立的 Agent Loop：统一 message/tool/usage/stop 契约与结构化错误，
> JSON Schema 工具校验失败回传模型实现自修正，同轮工具并发执行并按调用序回灌，
> 全程 span 级 Trace 记录；配套单测/集成测试与测试再生的 Trace artifact 证据。

- 证据：T1 行；`internal/agent`、`internal/tool`、`internal/trace`；PR #2。

**2. 实现 Trace-first 的可观测与评测链路**

> 实现 Trace-first 观测链路：span 模型 -> SQLite 持久化 -> OpenTelemetry SDK 导出
> （内容安全属性过滤，不导出 prompt 与工具参数）-> 稳定时间线回放；并构建版本化
> fixture Eval 套件，对真实 Runner 输出成功率、步数、token 桶与 P50 延迟报告。

- 证据：E1 行；`internal/observability`、`internal/eval`；PR #6。
- 口头补充时注明：Eval 用 fixture Provider，5 个场景，非 live 评测。

## 需要加“基于 fixture/本地验证”限定

**3. 多 Provider 流式适配与 MCP 接入**

> 基于 OpenAI Responses 与 Anthropic Messages 协议实现流式适配层：SSE 有序聚合、
> provider 专属 continuation 透明回传、错误归一化与有界重试（含 Retry-After）；
> 并实现 MCP stdio 客户端的发现/调用/取消/清理（本地 fixture 验证，未接真实服务）。

- 证据：T2 行；`internal/provider`、`internal/mcp`；PR #3。
- 必须带限定：live OpenAI/Anthropic/第三方 MCP 未记录，保持 partial。

**4. 上下文工程与分层记忆（数字基于 deterministic fixture）**

> 实现结构化上下文压缩（保留最近轮次与工具配对）、SQLite session/project/long-term
> 分层记忆（BM25 + 向量余弦 + RRF 混合召回）、大输出落盘与按范围取回；本地验证中
> 压缩 362->176 估算 token、大输出 4096->327 bytes 注入元数据。

- 证据：C1 行；`internal/context`、`internal/memory`、`internal/output`；PR #4。
- 必须带限定：summarizer/embedding 为 deterministic fixture，token 为 4 字符估算而非
  provider tokenizer。

**5. 本地 Multi-Agent 编排（本地 Worker fixture）**

> 实现持久化任务状态机与 Sub-Agent 编排：git worktree 隔离、JSON 子进程 Worker、
> 失败重试、带理由的规则路由与 fan-out 确定性仲裁，任务/attempt 状态与 Trace
> 落 SQLite（本地 Worker fixture 验证）。

- 证据：O1 行；`internal/orchestration`；PR #5。
- 必须带限定：Worker 为本地测试可执行文件，非 OpenCode/Codex live 兼容；不自动合并。

## 当前不能说

- live OpenAI/Anthropic/第三方 MCP 兼容性（smoke 未运行，无凭据）。
- 真实 summarizer / embedding 质量或压缩的 tokenizer 级数字。
- OpenCode/Codex Worker live 兼容。
- 远程 OTLP / Collector 上报。
- 生产级、高可用、分布式、统计显著性、模型选型结论、货币成本。

以上任何一条都只能以“planned/partial + 未验证”的口径讨论，见
[ROADMAP.md](../ROADMAP.md)。
