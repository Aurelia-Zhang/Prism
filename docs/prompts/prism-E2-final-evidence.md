# Prism E2 Final Evidence Prompt

你是 Prism Roadmap E2 的收尾 Agent，使用 GLM。Session 名称固定为：
prism-E2-final-evidence。

工作目录：/Users/aurelia/career/Prism

先读取：
- AGENTS.md
- .agents/skills/prism-mr/SKILL.md
- ROADMAP.md
- docs/resume-evidence.md
- README.md
- docs/evidence/ 下 T1、T2、C1、O1、E1 的全部报告
- docs/prompts/ 下已有 Prompt
- Git log 和已合并 PR #1～#6

开始：

git switch main
git pull --ff-only origin main
git status --short --branch
git switch -c docs/e2-final-evidence

目标：完成 Prism 的最终事实审计、指标汇总、README、简历证据和面试材料。

这是个人秋招项目收尾，不是继续开发基础设施。

In scope：

1. 最终事实审计
逐条检查 README 和 docs/resume-evidence.md 中的主张是否能对应到：

- 当前代码路径；
- 测试名称；
- Trace 或 evidence artifact；
- commit / PR；
- planned、partial 或 verified；
- 已知限制。

fixture、deterministic embedding、local Worker、in-memory OTel 不能写成 live 兼容性。
缺少真实 Provider/外部系统证据的能力保持 partial。

2. 最终指标报告
创建：

- docs/metrics/final-report.json
- docs/metrics/final-report.md

只汇总现有 artifact 中已经真实产生的数字，例如：

- C1 context：362 -> 176 estimated tokens；
- C1 large output：4096 -> 327 injected bytes；
- C1 retrieval：BM25/vector/hybrid Hit@1；
- O1 task retry、worktree、routing 和 arbitration 场景；
- E1：5 cases、success rate、average steps、tokens、P50 latency；
- T1 并行工具、Trace 和顺序回灌的已验证行为。

不要为了增加对比临时开发新 benchmark。
如果某项没有 baseline，就写“未提供可比 baseline”，不要编数字。
JSON 是事实源，Markdown 中的数字必须与 JSON 一致，并注明 fixture/live、样本量和限制。

3. README
重写为适合面试官快速阅读的最终 README，包含：

- Prism 是什么；
- 当前真实能力；
- 简洁架构流程；
- Quickstart；
- 一条可复现 Demo/测试命令；
- Roadmap 状态；
- 指标摘要；
- 已知限制。

不要把 planned/partial 写成已完整交付。
不要写生产级、企业级、高可用等夸张表述。

4. 简历证据
完善 docs/resume-evidence.md，确保每条主张链接：

- code；
- test；
- evidence；
- PR；
- limitation。

新增 docs/resume-bullets.md，提供 3～4 条可直接用于秋招简历的真实措辞。
每条控制在正常简历 bullet 长度，不堆砌所有技术名词。

必须区分：
- 可以直接说；
- 需要加“基于 fixture/本地验证”限定；
- 当前不能说。

5. 面试深挖
创建 docs/interview-deep-dive.md，用简洁问答覆盖：

- 为什么做 Agent Harness，而不是普通聊天 Demo；
- Provider-neutral Agent Loop；
- streaming 与 provider-specific continuation；
- JSON Schema Tool、自修正、并行执行和顺序回灌；
- Trace-first、持久化、回放和 Eval；
- context compression、分层记忆、RRF 和大输出管理；
- task state machine、Sub-Agent、worktree、retry、routing 和 arbitration；
- OpenTelemetry 脱敏；
- 指标局限；
- 为什么没做 HA、Swarm、外部向量库和完整生产安全系统。

每个问题只写面试时能讲清楚的核心答案，并链接关键代码或 evidence。
不要写成长篇架构论文。

6. AI 协作记录
创建 docs/ai-collaboration.md，只记录真实发生的协作：

- B0～E2 Session 名称；
- 使用的模型分工；
- 对应 Prompt 文件；
- PR #1～#6；
- 实现 Agent、总控 Review、修复、合并的流程；
- fixture/live 和证据状态原则。

禁止补写不存在的 Review、模型讨论或 live 测试。

Out of scope：

- 新增 Provider、Memory、Orchestration、OTel 或 Eval 功能；
- 为了让数字好看修改 dataset；
- 新 benchmark、大规模压测、统计显著性；
- live Provider、付费模型或外部服务调用；
- 发布 release；
- 修改用户外部简历或求职平台；
- 生产级安全、HA 和分布式能力。

状态要求：

- E2 文档和最终报告完成后可以标记 verified。
- T2 外部 Provider/MCP 兼容性仍按现有证据保持 partial。
- C1 真实 summarizer/embedding 质量保持 partial。
- O1 OpenCode/Codex live Worker 兼容性保持 partial。
- E1 远程 OTLP、live Provider 和 Judge 保持 partial。

验证只做一次：

go test ./...
go test -race ./...
go vet ./...
go build ./cmd/prism
git diff --check

另外检查：
- final-report.json 与 Markdown 数字一致；
- README 和 docs 中的本地链接存在；
- PR #1～#6 链接真实；
- 没有 API key、token、邮箱、完整 prompt 内容或其他敏感信息；
- 工作树干净。

保存本 Prompt：
docs/prompts/prism-E2-final-evidence.md

允许 commit、push，并创建 PR：

标题：E2: finalize metrics and interview evidence
base：main

禁止 merge、force-push、修改核心实现或发布 release。

最终汇报：

- 新增/修改文档；
- claim audit 结果；
- final-report 关键数字；
- 最终简历 bullets；
- verified/partial 状态；
- 未运行的 live 能力；
- 验证结果；
- commit、branch、PR URL。

创建 PR 后停止，等待总控最终 Review。
