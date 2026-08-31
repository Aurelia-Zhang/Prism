# Implementation Prompt: prism-T2-providers-mcp

你是 Prism Roadmap T2 的实现 Agent。模型使用 Luna High，Session 名称固定为
`prism-T2-providers-mcp`。同一 Session 继续处理总控 Review 后的修复，不要为修复另开 Session。

工作目录必须是 `/Users/aurelia/career/Prism`。不要进入 Codex 的隐藏 worktree。

## 开始闸门

先读取 `AGENTS.md`、`.agents/skills/prism-mr/SKILL.md`、`ROADMAP.md`、
`docs/resume-evidence.md` 和 T1 的 `internal/provider`、`internal/trace`、`internal/tool`、
`internal/agent`。

执行并确认：

```bash
pwd
git status --short --branch
gh pr view 2 --json state -q .state
```

只有 PR #2 状态为 `MERGED` 且工作区干净时才能开始。否则停止并报告，不要从未合并的
T1 分支创建 stacked PR。

首次开始时：

```bash
git switch main
git pull --ff-only origin main
git switch -c feat/t2-providers-mcp
```

后续修复继续使用已有分支和 PR。

## 背景与当前事实

T1 已提供 Provider-neutral Message、ordered OutputItem、ToolCall/ToolResult、Usage、
StopReason、structured Error、Agent Loop、Tool Registry 和 in-memory Trace。T2 在这些契约上
增加真实 Provider Adapter、统一 streaming/retry/error 行为和 MCP stdio。允许为 T2 必要行为
小幅扩展契约，但不得绕开或复制一套平行 Runner。

丢失第一版的 OpenAI Responses 设计仅是参考：SSE text/arguments 聚合、usage/error 映射、
reasoning item 的 id/summary/encrypted_content 回传、有序 OutputItem、终态优先级和 output item
去重。必须通过当前实现和测试重新建立证据。

## 单一目标

让同一个 T1 Agent Loop 能使用 OpenAI Responses、Anthropic Messages 或 MCP stdio 工具，
并通过统一事件、错误和有界 retry 契约运行。

## In scope

### 1. Provider-neutral streaming 扩展

保留现有 `Provider.Complete` 兼容路径。新增最小 streaming 能力，建议使用可选接口：

- `StreamingProvider`：返回最终聚合 `Response`，同时向 `EventSink` 发规范化事件；
- `StreamEvent` 至少包含 text delta、tool-call arguments delta、completed item、usage；
- Runner 通过向后兼容的 variadic option 或等价小扩展接收 EventSink；
- 非 streaming Fake Provider 和 T1 测试继续工作。

Provider-specific continuation item 必须由 Loop 透明保存并回传，但 Loop 不解释。可新增
opaque provider item，至少保存 provider、type 和原始 JSON。它用于 OpenAI reasoning item 和
Anthropic thinking/signature 等 continuation 数据；不得把具体协议字段散落进 Agent Loop。

保持所有文本、tool call 和 opaque item 的原始顺序。深拷贝新增的 RawMessage。

### 2. OpenAI Responses Adapter

实现真实 `/v1/responses` Adapter，支持：

- 可注入 base URL、HTTP Client、model 和 API key；
- 请求消息和稳定排序工具定义映射；
- SSE 分帧，能够处理任意网络 chunk 边界、多行 data 和终止事件；
- text delta 聚合；
- function call arguments 跨事件聚合；
- 多个 output item 的有序组装和按 item ID 去重；
- usage 映射到 T1 token buckets；
- API error 和 stream protocol error 映射；
- reasoning item 的 id、summary、encrypted_content 作为 opaque continuation 回传下一轮；
- response.completed、response.failed、response.incomplete 与错误事件的明确优先级。

不要实现 OpenAI SDK 的完整替代品，只覆盖 Prism 当前真实使用的 Responses 子集。HTTP fixture
必须来自可读的协议事件，不得只 mock Adapter 返回值。

### 3. Anthropic Messages Adapter

实现真实 Messages streaming Adapter，支持：

- 可注入 base URL、HTTP Client、model、API key 和 anthropic-version；
- content_block_start/delta/stop、message_start/delta/stop；
- text_delta 和 input_json_delta 聚合；
- tool_use 顺序和 ID 保留；
- usage 与 cache-read/cache-creation token 映射；
- thinking/signature 等需要回传的数据使用 opaque continuation；
- API 和 stream error 统一映射。

只实现 Prism 当前路径所需子集，并把不支持的 block 类型作为明确限制或结构化错误，不能静默
丢弃会影响后续请求的内容。

### 4. 统一错误和 retry

至少规范化：

- authentication
- permission
- invalid_request
- rate_limit
- context_length
- content_filter
- network
- timeout
- server_error
- stream_protocol

Retry 必须：

- 有最大次数和最大总等待预算；
- 仅对明确 retryable 错误生效；
- 尊重 context cancellation/deadline；
- 支持 Retry-After；
- backoff 和 sleeper 可注入，测试不得真实 sleep；
- streaming 已经对外发出 delta 后不自动重试，避免重复输出；如果选择先缓冲再发布，必须说明
  延迟与 streaming 体验取舍。

每次尝试产生可追踪属性：provider、model、attempt、error.code、retryable。只扩展现有 Trace，
不要实现 OTel Exporter。

### 5. MCP stdio Client

实现 MCP stdio 的当前稳定协议子集：

- initialize 与 initialized 生命周期；
- tools/list；
- tools/call；
- JSON-RPC request ID、result 和 error；
- process context cancellation、stderr 采集和退出处理；
- Server 工具加入现有 Registry；
- 使用稳定 namespace 解决多 Server 或本地工具重名；
- Server 退出后摘除它拥有的工具，不影响其他 Registry 工具。

优先使用维护中的官方 Go MCP SDK；若不用，最终报告必须说明原因和所实现协议边界。不要实现
resources、prompts、sampling、HTTP transport 或完整 MCP 平台。

MCP fixture server 可以用于自动测试，但不能冒充第三方兼容性。另提供一个可选真实 smoke：
连接公开维护的 MCP stdio Server；若机器没有 Node/依赖或无法运行，保留 `partial` 并明确报告，
不要自动全局安装工具。

## Out of scope

- C1 context compression、SQLite memory 和大输出存储；
- O1 Sub-Agent、任务状态机、worktree 和 routing；
- E1 OTel、Trace SQLite、回放和 Eval；
- OpenAI Chat Completions、Anthropic 非 streaming 全协议穷举；
- MCP HTTP/SSE transport、resources/prompts/sampling；
- Provider fallback、模型路由、生产级密钥系统；
- 伪造 live 请求、费用或兼容性。

## 测试与验证

必须使用 `httptest.Server` 和真实 SSE bytes 覆盖：分片边界、有序 item、arguments 聚合、
reasoning/thinking continuation、usage、错误映射、Retry-After、取消、partial stream 不重试。

MCP 测试必须启动真实子进程 fixture，走 stdin/stdout JSON-RPC，覆盖初始化、list/call、协议错误、
进程退出、工具摘除和取消。

增加 env-gated live smoke，默认测试不要求凭据：

- OpenAI：仅在明确环境变量存在时运行；
- Anthropic：同上；
- MCP：本地外部 Server 可用时运行。

live 输出必须脱敏，不提交 key、完整敏感 prompt 或未经允许的模型内容。

运行：

```bash
gofmt -w <changed-go-files>
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/prism
```

再运行 Adapter fixture、MCP subprocess 和可用的 live smoke 命令，逐项记录。

## 证据与状态

生成 `docs/evidence/t2/`：

- fixture summary：事件数、聚合 item、usage、retry attempts、错误码；
- MCP subprocess transcript，去除环境敏感信息；
- live smoke summary，仅记录真实执行的 provider/model、时间、结果和 usage；
- 明确区分 fixture、local subprocess、live external。

只有 OpenAI、Anthropic 和外部 MCP 三项真实 smoke 都成功，T2 才能整体 `verified`。任何一项缺失，
T2 标记 `partial`，但分别记录已验证部分。更新 `docs/resume-evidence.md`、README 和 Roadmap，
不得把 fixture 当 live。

将本 Prompt 原文保存为 `docs/prompts/prism-T2-providers-mcp.md`。

## Git 权限与交付

允许在 `feat/t2-providers-mcp` commit、push、创建 PR；禁止 merge、force-push、改写已发布历史、
删除分支或修改其他 Roadmap 功能。PR base 为 `main`，标题：
`T2: add streaming providers and MCP stdio`。

最终汇报：实现范围、契约变化、文件、测试逐项结果、fixture/live 证据、依赖与理由、真实参考路径、
状态变化、限制、commit、branch、PR URL。创建 PR 后停止，等待总控 Review。
