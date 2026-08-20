# Roadmap

实现顺序：**M1 → M4 的 Trace 部分 → M2 → M3 → M4 的评测部分**。

Trace 埋点提前到第二步，是因为趁 Loop 还简单时埋点最省事；等上下文压缩和多 Agent
都上了再补埋点，等于把最复杂的路径反过来重走一遍。评测放最后，因为需要前面都能跑
才有东西可评。

---

## M1 · Agent Loop 与工具执行

- [ ] `provider` 包：统一 `Message` / `ToolCall` / `Usage` / `StopReason` 模型，
      实现 OpenAI-compatible 与 Anthropic Messages 两个 adapter，含流式解析
- [ ] 错误模型统一：rate limit、上下文超限、内容拦截、网络错误映射为同一组错误码
- [ ] `agent` 包：主循环与判停条件（无工具调用 / 达到最大轮数 / 用户取消 / 致命错误）
- [ ] 同一轮内多个 tool_use 并发执行（errgroup + 结果按原顺序回灌）
- [ ] `tool` 包：Registry 按名注册，调用前做 JSON Schema 校验，
      校验失败作为工具错误回灌而非中断循环
- [ ] 内置工具：`read_file` / `write_file` / `bash` / `grep`
- [ ] MCP Client：stdio transport，启动时 `list_tools` 并入 Registry，
      Server 挂掉时自动摘除该组工具

**验收**：同一任务分别用 OpenAI 与 Anthropic 跑通；外接一个 MCP Server 并被模型真实调用。

## M2 · 上下文工程与分层记忆

- [ ] 上下文装配器：system prompt 与 tool definitions 作为稳定前缀，
      不因历史变化而重排；工具定义稳定排序
- [ ] Token 预算管理：估算当前占用，超阈值触发压缩
- [ ] 结构化压缩：产出「已完成 / 未完成 / 关键文件 / 关键结论」摘要，保留最近 N 轮原文；
      整段替换靠前历史，不零散删中间轮次
- [ ] 工具输出分级留存：大体量结果只回灌摘要 + ID，原文落盘，
      提供 `fetch_output(id, range)` 按需回取
- [ ] 记忆分层落 SQLite：会话内 / 项目级 / 长期，各自的写入时机与生命周期
- [ ] 混合召回：BM25（FTS5）+ 向量余弦，注入位置固定在稳定前缀之后、历史之前
- [ ] 缓存命中率度量：`cache_read_input_tokens` / `cache_creation_input_tokens` 落 span 属性

**验收**：一次超出上下文窗口的长任务，压缩前后的 Token 曲线与压缩产物；
关闭 / 开启记忆的同任务效果对比。

## M3 · Multi-Agent 编排与任务调度

- [ ] `spawn_task` 工具：委派子任务，子 Agent 独立上下文 + 独立 git worktree，主循环不阻塞
- [ ] 结果回灌口径：只回灌结构化结果（结论 + 改动文件 + 失败原因），完整轨迹留在子 trace
- [ ] 任务状态机：queued / running / succeeded / failed / cancelled，
      状态与中间产物持久化，进程重启后可恢复
- [ ] 进度查询、主动取消、失败重试；结果异步回收进主会话
- [ ] fan-out 仲裁：同一任务多路策略并行求解，主 Agent 按断言或评分选优
- [ ] 模型路由：按任务复杂度选模型，路由原因写进 Trace
- [ ] 跨 Agent 归因：各 Agent 的耗时、Token 成本、失败原因挂在各自 span 上

**验收**：一个主任务派生三个子任务并发执行，其中一个人为失败并重试成功，展示完整状态流转。

## M4 · 可观测性与评测

- [ ] Trace 模型：一次 run 一个 trace；模型调用 / 工具调用 / 压缩 / 路由各成 span，
      记录耗时、Token（输入 / 输出 / 缓存命中）、错误码、模型名
- [ ] 按 OpenTelemetry `gen_ai.*` 语义约定导出，同时落一份 SQLite
- [ ] 回放：给定 trace id 重放整轮执行的时间线
- [ ] 评测集：20–30 个场景化用例（改代码 / 查信息 / 多步任务 / 工具失败重试），每例带断言
- [ ] 双重度量：规则断言 + LLM-as-Judge，两者都记录
- [ ] 回归流水线：一条命令跑全量评测，输出成功率、平均步数、平均 Token 成本、
      P50/P95 延迟；支持两个版本或两个模型的对比报告

**验收**：一份真实产出的评测对比报告，以及一张 Trace 时间线视图。

---

## 未来考虑

- TypeScript SDK / TUI
- 记忆量级到十万条时切换 sqlite-vec
- HTTP transport 的 MCP Server 支持
