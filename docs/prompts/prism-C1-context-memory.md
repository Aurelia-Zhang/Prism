# Prism Roadmap C1 Implementation Prompt

你是 Prism Roadmap C1 的实现 Agent，使用 DeepSeek。Session 名称：
`prism-C1-context-memory`。

工作目录：`/Users/aurelia/career/Prism`

先读取 `AGENTS.md`、`.agents/skills/prism-mr/SKILL.md`、`ROADMAP.md`、
`docs/resume-evidence.md`，以及 `internal/agent`、`provider`、`tool`、`trace`。

开始前执行：

```bash
git switch main
git pull --ff-only origin main
git status --short --branch
git switch -c feat/c1-context-memory
```

目标：以个人项目和秋招演示为标准，在现有 Agent Loop 中完成一条可运行的
“上下文压缩 → 分层记忆召回 → 大输出落盘/回取”主链路。

In scope：

1. 上下文压缩
- 超过可配置预算时压缩较早消息，保留最近 N 轮。
- 摘要固定包含 completed、pending、key_files、decisions、constraints。
- 不拆散 tool call/result。
- Summarizer 使用接口；测试使用 deterministic implementation。
- 产生 context.compact Trace，记录压缩前后估算 token/message 数。

2. SQLite 分层记忆
- 一个 SQLite 文件，支持 session、project、long-term 三种 scope。
- 实现写入、删除、scope 隔离和 top-k 检索。
- 使用 FTS5 BM25 + float32 cosine，并用简单 RRF 融合。
- Embedding 使用接口；测试使用 deterministic vectors。
- 只做 schema version 1，不建设通用 migration framework。

3. 大工具输出
- 超过阈值时把完整输出写入可配置目录，并在 SQLite 保存 ID、hash、size、path。
- Agent Loop 只回灌摘要、output ID 和原始大小。
- 提供 fetch_output(id, offset, limit) Tool。
- 小输出保持 T1 行为。
- 测试全部使用 t.TempDir()。

4. Runtime 集成
- 使用可选配置接入现有 Runner；未配置时 T1/T2 行为不变。
- memory.recall、context.compact、output.persist 进入现有 Trace。
- 不重写 Provider、Runner 或 Trace 契约。

Out of scope：

- 真实 embedding 服务和真实摘要质量验证；
- 外部向量数据库；
- 通用 migration、复杂隐私系统、大规模压测；
- O1 Sub-Agent/worktree/routing；
- E1 OTel/replay/eval；
- 与主链路无关的协议边界和生产级抽象。

测试重点只覆盖：

- 压缩后保留关键字段和最近消息；
- tool call/result 不断裂；
- 三种 memory scope 隔离；
- BM25/vector/hybrid 能返回预期 top-k；
- 大输出落盘、回取和 hash 校验；
- Runner 未配置时兼容，配置后产生对应 Trace。

生成一个 `docs/evidence/c1/report.md`，记录：

- 同场景压缩前后估算 token/message 数；
- 大输出原始 bytes 与回灌 bytes；
- 一个小型人工 relevance 集上的 BM25/vector/hybrid Recall@k；
- 明确注明使用 deterministic summarizer/embedding，不是 live 质量。

不要为获得漂亮数字调整或伪造数据。C1 保持 partial，除非真实 Provider 摘要和
embedding 也得到验证。

最终只运行一次完整验证：

```bash
gofmt -w <changed-go-files>
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/prism
git diff --check
```

更新 `README.md`、`ROADMAP.md`、`docs/resume-evidence.md`，并保存本 Prompt 到
`docs/prompts/prism-C1-context-memory.md`。

允许 commit、push，并创建 PR：
标题：`C1: add context compaction and layered memory`
base：`main`

禁止 merge、force-push、修改其他 Roadmap 项。

完成后汇报：

- 实现范围和主要文件
- SQLite schema 与核心取舍
- 测试结果
- report.md 中的真实数字
- 状态与已知限制
- commit、分支和 PR URL

创建 PR 后停止，等待总控 Review。
