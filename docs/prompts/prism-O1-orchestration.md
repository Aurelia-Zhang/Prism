# Prism O1 Orchestration Prompt

你是 Prism Roadmap O1 的实现 Agent，使用 Luna High。Session 名称固定为：
`prism-O1-orchestration`。

工作目录：`/Users/aurelia/career/Prism`

目标：以个人项目和秋招 Demo 为标准，实现一条真实可运行的本地 Multi-Agent 主链路：

创建任务 -> 模型路由 -> 创建独立 git worktree -> 启动 Worker 子进程 ->
收集结构化结果 -> fan-out 仲裁 -> 持久化状态和 Trace。

In scope：

- queued/running/succeeded/failed/cancelled 状态机，集中转换校验，SQLite task/attempt/parent/strategy/status/progress/worktree/model/result/error/usage/timestamps。
- create/start/progress/complete/fail/cancel/retry；retry 保留旧 attempt 并创建新 attempt。
- 参数数组调用 `git` 创建明确 base ref 的独立 worktree/branch，记录 branch、HEAD、changed files、diff summary；不自动 merge，失败现场默认保留，只清理由当前 Manager 创建的 worktree。
- 小型 Worker 接口和 JSON stdin/stdout `CommandWorker`，支持 task ID、goal、constraints、worktree、model profile、budget、status、summary、changed files、validation、usage、error；context cancellation 终止子进程。
- parent 并发启动两个 strategy child；失败后 retry；确定性 score/reason Arbiter 选择 winner，不实现 LLM 仲裁或自动合并。
- fast/balanced/strong ModelProfile；Router 根据 complexity、tool requirement、token budget、retry history 返回 profile 和可读 reason；只记录 token usage。
- 复用现有 Trace，增加 `model.route`、`task.spawn`、`task.wait`、`task.retry`、`task.arbitrate`。
- 使用临时 Git repo 与当前 test executable fixture 测试；生成 `docs/evidence/o1/report.md`。

Out of scope：远程 Worker、分布式队列、HA、lease、leader election、Kubernetes、Agent 自由递归、自动 merge/冲突解决/GitHub 自动化、完整 OpenCode/Codex CLI 适配、真实多模型成本对比、OTel、回放和 Eval。

保持 O1 为 `partial`，直到真实 OpenCode/Codex Worker 验证完成。更新 README、ROADMAP、`docs/resume-evidence.md`。最终验证命令：

```bash
gofmt -w <changed-go-files>
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/prism
git diff --check
```

允许 commit、push、创建标题为 `O1: add persistent multi-agent orchestration`、base 为 `main` 的 PR；禁止 merge、force-push、修改其他 Roadmap 项。创建 PR 后停止，等待总控 Review。
