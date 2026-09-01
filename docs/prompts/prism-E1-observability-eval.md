# Prism E1 Observability Eval Prompt

你是 Prism Roadmap E1 的实现 Agent，使用 GPT。Session 名称固定为：
`prism-E1-observability-eval`。

工作目录：`/Users/aurelia/career/Prism`

开始：

```bash
git switch main
git pull --ff-only origin main
git status --short --branch
git switch -c feat/e1-observability-eval
```

目标：以个人项目和秋招 Demo 为标准，完成一条真实可运行的观测与评测主链路：

Runtime Trace → SQLite 持久化 → OpenTelemetry 导出 → 时间线回放 →
fixture 场景 Eval → JSON/Markdown 指标报告。

In scope：

1. Trace SQLite 持久化：新增 `internal/observability`，将现有 `trace.Snapshot` 原子写入
SQLite，保存 trace、span、parent、sequence、timestamps、status、attributes、usage 和
error，支持按 trace ID 读取，序列化 round-trip 不丢字段，复用当前 SQLite driver，只做
schema version 1。
2. OpenTelemetry 导出：使用官方 OpenTelemetry Go SDK 转换并导出现有 Prism spans；测试用
in-memory exporter 验证 span 名称、父子关系、status、usage 和关键 attributes。只使用
`prism.*` 自定义属性，不声称完整 GenAI semantic conventions。默认不导出 API key、完整
prompt、tool arguments/output、reasoning 内容；不要求本机安装 Collector，外部 OTLP 未运行
时标记 `partial`。
3. Trace 回放：只读取持久化 Trace 重建时间线，不重新调用 Provider 或 Tool。展示层级、span
name、duration、status、usage 和 error code，输出稳定可测试。CLI 为：
`prism trace show <db-path> <trace-id>` 与 `prism trace export <db-path> <trace-id>`；不存在
trace 返回非零退出码。
4. 场景 Eval：新增 `internal/eval`，定义版本化 Suite/Case：id、input、timeout、expected
status、required spans、expected text/error。Eval 消费真实 Runner Result 和 Trace。初版五个
fixture：普通文本完成、单工具调用、参数校验失败后自修正、C1 context.compact 或
output.persist、O1 failure/retry/routing。Fake Provider、deterministic summarizer/embedding
和本地 Worker 必须明确标注 fixture。
5. 指标报告：报告 case 数、成功率、平均 steps、input/output/cache tokens、P50 latency；小
样本不计算 P95，不做统计显著性结论。JSON 是事实源，Markdown 从同一结果生成。CLI 为：
`prism eval run <suite-path> --output <report.json>`。完整通用 suite loader 若扩大范围，可用
明确参数运行内置 fixture suite，不建设 Eval 平台。
6. Evidence：生成 `docs/evidence/e1/trace.json`、`replay.txt`、`otel.json`、
`eval-report.json`、`eval-report.md`；artifact 必须由测试或明确命令生成并校验一致性，不能
手写假数据。

Out of scope：Grafana、Jaeger UI、远程观测平台、Trace 重新执行或有副作用 Tool 重放、完整
GenAI semantic conventions、20+ 场景、大规模压测、统计显著性、LLM-as-Judge、多模型对比和
选型结论、E2 简历及面试材料、生产级日志权限和隐私平台、修改 E2。

测试重点：SQLite Trace round-trip；replay 顺序和父子层级；OTel span mapping 与敏感字段不
导出；五个 fixture cases；指标计算和 JSON/Markdown 一致；CLI 成功及 trace 不存在时的退出
码；原有 T1/T2/C1/O1 测试不受影响。

最终验证命令：

```bash
gofmt -w <changed-go-files>
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/prism
git diff --check
```

允许 commit、push，并创建标题为 `E1: add trace export replay and evaluation`、base 为
`main` 的 PR；禁止 merge、force-push、修改 E2。创建 PR 后停止，等待总控 Review。
