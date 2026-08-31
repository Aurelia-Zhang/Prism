# Implementation Prompt: prism-T1-runtime

你是 Prism Roadmap T1 的实现 Agent。

Session 名称：prism-T1-runtime
工作目录必须是：/Users/aurelia/career/Prism

你负责实现、测试、commit、push 和创建 PR。不要 merge PR。完成后把 PR URL 和验证结果交回总控 Codex Review。

一、开始前检查

先执行：

pwd
git status --short --branch
git remote -v

必须确认：

1. 当前目录是 /Users/aurelia/career/Prism。
2. 工作区没有用户未提交的改动。
3. origin 指向 Aurelia-Zhang/Prism。

如果工作区不干净，停止并报告，不要覆盖已有修改。

首次开始 T1 时执行：

git switch main
git pull --ff-only origin main
git switch -c feat/t1-runtime

如果 feat/t1-runtime 已经存在，说明这是同一 Session 的后续修复，继续使用原分支，不要重新建分支。

不要在 /Users/aurelia/.codex/worktrees/ 下修改代码，那是总控 Codex 的隔离工作区。

开始实现前完整阅读：

- AGENTS.md
- .agents/skills/prism-mr/SKILL.md
- ROADMAP.md
- docs/resume-evidence.md
- README.md
- go.mod
- cmd/prism/main.go

当前仓库事实：

- B0 已合并。
- 当前只有 help/version CLI。
- Trace、Provider Runtime、Agent Loop 和 Tool Registry 尚未实现。
- /Users/aurelia/career/refer-projects 下存在 Codex、pi、DeepSeek Harness 源码，只能作为设计参考，不能冒充 Prism 已实现证据。
- 第一版丢失前的 Trace、Runner 和 OpenAI Responses 设计只能作为参考。
- 本 MR 不验证 OpenAI、Anthropic 或 MCP 兼容性。

二、单一目标

完成 T1：

实现一个 Provider-neutral Agent Loop，并将真实执行路径接入并发安全的内存 Trace Recorder 和 JSON Schema Tool Registry。

必须跑通：

用户消息
→ Provider 调用
→ 返回有序 ToolCall
→ JSON Schema 校验
→ 同轮工具并行执行
→ ToolResult 按原调用顺序回灌
→ Provider 继续生成
→ 正常结束或结构化失败
→ 完整 Trace

三、In scope

1. Provider-neutral 契约

建立小而清晰的 Go package，建议使用：

- internal/provider
- internal/trace
- internal/tool
- internal/agent

如果仓库现状证明其他目录更合理，可以调整，但必须在最终报告解释。

Provider-neutral 契约至少覆盖：

- Message 和 Role
- 有序 OutputItem
- Text OutputItem
- ToolCall
- ToolResult
- Usage
- StopReason
- 结构化 Error

必须保留 Provider 输出项的原始顺序。

Usage 至少预留：

- input tokens
- output tokens
- cache-read tokens
- cache-write/creation tokens

不要把 OpenAI 或 Anthropic 的协议字段泄漏进 Agent Loop。

2. Trace Recorder

实现并发安全的内存 Recorder：

- 一次 Run 对应一个 root span：agent.run
- 每次 Provider 调用产生 model.call span
- 每次工具处理产生 tool.call span
- 显式 ParentSpanID
- running / ok / error 生命周期
- 结构化错误：code、message，以及确有需要的 retryable 信息
- Token usage 分桶
- 独立、单调递增的 start sequence 和 end sequence
- Span 只能结束一次；重复结束采用 first-wins，不能覆盖第一次终态
- Snapshot 必须深拷贝，调用方修改 Snapshot 不能污染 Recorder 内部状态
- Recorder 必须通过 go test -race

允许注入 Clock 和 ID Generator，方便生成确定性测试和 Trace 证据；不要因此构建大型依赖注入框架。

Run 返回以后，该 Run 创建的 Span 不得残留 running 状态。

3. Tool Registry

每个 Tool 至少包含：

- name
- description
- JSON Schema
- Handler(ctx, arguments)

要求：

- Tool 名称唯一
- 注册时检查名称和 Schema
- Tool definitions 返回稳定顺序
- 调用前校验 arguments
- 非法 JSON、Schema 不匹配、Tool 不存在都返回结构化 ToolResult
- 参数校验失败时不能调用 Handler
- 参数校验失败和普通 Tool Handler 失败不能直接中断 Agent Loop，应作为 ToolResult 回灌，让模型下一轮自行修正
- context cancellation 是终止信号，不能伪装成普通 Tool 失败

如需 JSON Schema 依赖，只引入一个专注、维护正常的 Go module。最终报告写明选择原因，不要引入完整 Agent Framework。

4. Agent Loop

Agent Loop 必须支持：

- context cancellation
- MaxRounds
- Provider fatal error
- 正常无 ToolCall 结束
- 同一轮多个 ToolCall 并行执行
- ToolResult 按原 ToolCall 顺序回灌
- Tool validation/handler error 回灌后继续下一轮
- root、model.call、tool.call Span 的正确父子关系和终态

明确规定：MaxRounds 表示允许的最大 Provider 调用次数，避免 off-by-one 歧义。

达到 MaxRounds、Provider fatal error 或 cancellation 时：

- 返回结构化错误
- 正确结束 root/model/tool Span
- 不得遗留 running Span

并发测试不要依靠“sleep 多少毫秒所以应该更快”这种易抖动断言。使用 channel、barrier 或可控 Handler 证明多个 Tool 确实同时启动；再让它们逆序完成，验证回灌仍保持调用顺序。

四、Out of scope

本 MR 不实现：

- OpenAI HTTP / Responses Adapter
- Anthropic Messages Adapter
- SSE streaming
- rate limit 或网络 retry
- MCP
- SQLite
- 上下文压缩和记忆
- 大工具输出落盘
- Sub-Agent、任务状态机或 git worktree 调度
- OpenTelemetry Exporter
- Trace 持久化和回放
- Eval Pipeline
- read_file、write_file、bash 等内置工具
- HA、分布式一致性、完整安全体系
- 无方法说明的性能数字

不要为了以后可能需要而提前实现这些能力。

五、必须复用和保护的契约

- 不破坏 cmd/prism 的 help/version 行为。
- 遵守 AGENTS.md 中 planned / partial / verified 的证据定义。
- README 只能描述当前可复现的能力。
- Fake Provider 只能证明内部 Agent Runtime 行为，不能证明 OpenAI/Anthropic 兼容。
- T2 会在 T1 契约上实现 Provider Adapter，因此 T1 类型需要清楚，但不要猜测并实现完整 Provider 协议。
- 保持实现适合个人面试项目，不构建生产级框架。

可以按需查阅参考仓库，但只阅读与当前决策直接相关的文件。最终报告列出真正查阅过的路径和借鉴点；没有阅读就不要声称参考过。

六、测试要求

至少覆盖：

Trace：

- parent/child 关系
- running → ok/error
- first-wins 终止
- start/end sequence
- usage 聚合或记录
- Snapshot 深拷贝
- 并发读写
- Run 返回后没有 running Span

Tool：

- 正常注册和调用
- 重名
- 非法 Schema
- 非法 JSON arguments
- Schema 校验失败
- Tool 不存在
- Handler error
- validation failure 不调用 Handler
- context cancellation

Agent Loop：

- 无 ToolCall 正常结束
- 单工具调用和结果回灌
- 多工具并行启动
- 工具逆序完成但结果保持调用顺序
- 参数错误回灌后 Fake Provider 下一轮修正并成功
- Tool Handler 失败回灌
- Provider fatal error
- cancellation
- MaxRounds 边界
- model.call/tool.call Span 的父子关系、状态和 usage

Fake Provider 必须是可编程的测试替身，不能假装真实 Provider。

七、Trace 证据

产出一份确定性的、机器可读且人能阅读的 T1 Trace artifact。

建议放在：

docs/evidence/t1/trace.json

它必须由真实 Agent Loop 测试路径序列化得到，至少展示：

- agent.run
- 两次 model.call
- 一个参数校验失败的 tool.call
- 一轮多工具调用
- ToolResult 原序回灌
- Span parent/child
- status
- usage
- start/end sequence

测试必须把实际运行结果与该 artifact 对比，或提供一个明确、可重复的生成命令。

禁止手写 JSON 后把它冒充运行产物。

T1 不要求性能指标。不要伪造延迟、Token 成本或 Provider 数据。

八、文档与证据更新

完成实现后更新：

ROADMAP.md：

- T1 的全部验收证据满足时改为 verified。
- 如果缺少要求中的一项，改为 partial，并明确缺什么。
- T2、C1、O1、E1、E2 状态不要提前修改。

docs/resume-evidence.md：

- Provider-neutral Agent Loop 行：填写代码路径、测试名和已知限制。
- JSON Schema/并行工具执行行：填写代码、测试和 Trace artifact。
- Trace/OTel/回放共用行只能改为 partial，因为本 MR 只有内存 Trace，没有 OTel、持久化或回放。
- OpenAI、Anthropic 和 MCP 仍保持 planned。

README.md：

只能新增以下已验证事实：

- Provider-neutral 内部 Agent Loop
- JSON Schema Tool Registry
- 同轮工具并行、结果有序回灌
- 内存 Trace

必须明确真实 Provider Adapter 和 MCP 尚未实现。

同时把 docs/prompts/prism-T1-runtime.md 更新为本次实际收到的最终 Prompt，作为真实 AI 协作记录。不要伪造 Session、Review 或实现过程。

九、验证命令

运行并报告：

gofmt -w <本 MR 修改的 Go 文件>
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/prism
git diff --check

如果 go 不在 PATH，可以使用 /usr/local/go/bin/go，但需要在最终报告说明。

还要运行 Trace artifact 的生成或一致性校验命令。

任何命令没运行或失败，都必须如实报告。

十、Git 和 PR 权限

你被允许：

- 在 feat/t1-runtime 分支修改 T1 范围内文件
- commit
- push 到 origin
- 创建 GitHub PR

你不被允许：

- merge PR
- force-push
- rebase 或重写已发布历史
- 修改 main
- 删除分支
- 修改 T1 之外的功能
- 把未验证能力写成 verified

建议 Commit 保持 1～3 个有意义的提交，不要为每个小改动产生碎片 Commit。

PR：

- base：main
- head：feat/t1-runtime
- 标题：T1: implement trace-first agent runtime
- 使用 .github/pull_request_template.md
- Metrics 对 T1 填 not applicable，除非确实产出了有方法说明的真实数据
- 明确写 Fake Provider 不等于真实 Provider 兼容性

十一、最终汇报格式

完成后只按下面格式汇报：

1. 实现范围
2. 关键契约与技术取舍
3. 修改文件
4. 测试命令及逐项结果
5. Trace artifact 路径与生成方式
6. Roadmap/resume-evidence 状态变化
7. 新增依赖及选择原因
8. 实际查阅的参考源码路径及借鉴点
9. 已知限制和未验证兼容性
10. Commit hash、分支和 PR URL

创建 PR 后停止，不要 merge，等待总控 Codex Review。
