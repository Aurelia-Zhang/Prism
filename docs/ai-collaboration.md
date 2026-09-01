# AI Collaboration Record

只记录仓库中可验证的真实协作事实：Prompt 文件、Session 名称、分支、commit、PR 和
合并记录。不补写未发生的 Review 细节、模型间讨论或 live 测试。

## 流程

每个 Roadmap item 走同一条链路：

1. 总控编写实现 Prompt（明确 goal / in scope / out of scope / 验证命令 / 证据要求 /
   Git 权限），Prompt 原文保存在 `docs/prompts/`；
2. 实现 Agent 在独立 feature branch 上实现、测试、commit、push，并创建 PR（不允许
   merge）；
3. 总控 Review；需要修复时由同一 Session 在同一分支继续修复（各 PR 中的 `fix:` 类
   commit 即修复记录）；
4. Review 通过后由 owner 账号合并到 `main`（squash merge commit 可在 main 历史查证）。

## Session 与模型分工

| Item | Session | 实现模型（以 Prompt 记录为准） | Prompt 文件 | 分支 | PR（合并时间 UTC） |
|---|---|---|---|---|---|
| B0 | 未在仓库记录 | 总控完成（无独立实现 Prompt） | 无（B0 创建了 `docs/prompts/` 并产出 T1 Prompt） | — | [PR #1](https://github.com/Aurelia-Zhang/Prism/pull/1)（2026-08-31 10:34） |
| T1 | `prism-T1-runtime` | Prompt 未标注实现模型 | [prism-T1-runtime.md](prompts/prism-T1-runtime.md) | `feat/t1-runtime` | [PR #2](https://github.com/Aurelia-Zhang/Prism/pull/2)（2026-08-31 13:49） |
| T2 | `prism-T2-providers-mcp` | Luna High | [prism-T2-providers-mcp.md](prompts/prism-T2-providers-mcp.md) | `feat/t2-providers-mcp` | [PR #3](https://github.com/Aurelia-Zhang/Prism/pull/3)（2026-08-31 14:35） |
| C1 | `prism-C1-context-memory` | DeepSeek | [prism-C1-context-memory.md](prompts/prism-C1-context-memory.md) | `feat/c1-context-memory` | [PR #4](https://github.com/Aurelia-Zhang/Prism/pull/4)（2026-08-31 15:06） |
| O1 | `prism-O1-orchestration` | Luna High | [prism-O1-orchestration.md](prompts/prism-O1-orchestration.md) | `feat/o1-orchestration` | [PR #5](https://github.com/Aurelia-Zhang/Prism/pull/5)（2026-08-31 15:31） |
| E1 | `prism-E1-observability-eval` | GPT | [prism-E1-observability-eval.md](prompts/prism-E1-observability-eval.md) | `feat/e1-observability-eval` | [PR #6](https://github.com/Aurelia-Zhang/Prism/pull/6)（2026-09-01 02:15） |
| E2 | `prism-E2-final-evidence` | GLM | [prism-E2-final-evidence.md](prompts/prism-E2-final-evidence.md) | `docs/e2-final-evidence` | PR #7（本 MR，创建待 Review） |

总控使用 Codex（T1/E1 Prompt 中的“总控 Codex Review”与总控隔离 worktree 约定可查证）。

## Review 与修复的可验证记录

各 PR 内除首个实现 commit 外的后续 commit 即总控 Review 后的修复：

- PR #2：`2579229` fix: tighten T1 runtime control flow
- PR #3：`ba49b89` T2: fix provider compatibility and MCP cleanup
- PR #4：`27a57cf` fix: verify persisted output integrity、`e8fdd03` docs: link C1 evidence commit
- PR #5：`eaa0c2b` docs: link O1 evidence
- PR #6：`4d462f5` E1: isolate orchestration eval repository

## fixture/live 与证据状态原则

- 每条能力主张必须处于 `planned` / `partial` / `verified` 三态之一（定义见
  [AGENTS.md](../AGENTS.md)）；fixture 可以验证内部运行时行为，但不能建立 live
  OpenAI/Anthropic/MCP/OpenCode/Codex/远程 OTLP 兼容性。
- live smoke（`OPENAI_API_KEY`、`ANTHROPIC_API_KEY`、`PRISM_MCP_EXTERNAL_COMMAND`）
  为 opt-in，默认环境未提供凭据，因此 T2 外部兼容性保持 `partial`；对应声明见
  [evidence/t2/live-smoke-summary.md](evidence/t2/live-smoke-summary.md)。
- artifact 必须由测试或明确命令再生并校验（如 `TestTraceArtifact`、
  `TestE1EvidenceArtifacts`、`TestMCPScenarioArtifact`），不接受手写数据。
- 最终指标只汇总已有 artifact 中的真实数字，无 baseline 的项明确写
  “未提供可比 baseline”，见 [metrics/final-report.json](metrics/final-report.json)。
