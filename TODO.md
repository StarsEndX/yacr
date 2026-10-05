# YACR — Yet Another Code Reviewer

## 背景与问题

与 coding agent 协作开发的工作流：agent 完成任务 → 用户简单验证（非 review）→ commit；多轮任务累积多个领先 commit 不 push。到临界点发起 review 时，真正的痛点：

- 在 agent 会话里直接 review：diff 难看懂、交互不方便、可靠性难保证
- 逐 commit 看没有意义：**真正需要 review 的是领先提交的综合 diff**
- 隐蔽风险：agent 中途的多余/未说明改动（用户要求改 A1，agent 顺带改了 A2、A3，混在一起 commit）跨 commit 后很难被发现

## 目标

做一个 Go 工具，对**功能分支领先开发分支的多个 commit 的综合 diff** 做 AI 辅助 code review：

1. **行级解释保证**：每一处变更行都有对应的解释；连贯的变更块可以合并为一条解释，但锚定必须精确到行
2. **意图由 agent 定义**：工具不规定"什么是意图"，只负责把变更事实精确暴露（diff/行定位/commit），让用户的 agent 自行追踪并撰写解释
3. **固定报告接口**：报告不依赖 agent 的文件编辑能力，只能通过工具接口（CLI/MCP）变更；工具负责校验：行定位不正确、commit hash 不存在、有变更未被解释等
4. **agent 无关**：工具供任意 agent 使用（CLI / MCP），不内置调起特定 agent
5. **人类 TUI 只读**：渲染报告 + diff + 覆盖情况，支持阅读浏览，不提供报告编辑
6. **实时问答**：用户开着 agent 会话（如 Claude Code）时，agent 可通过接口查询任意位置的变更事实与已有解释，回答"这里为什么这么改"等问题

不做：节点 1-2（agent 交互、commit 管理）；merge 处理（真实 merge 由 CI 完成，范围内是线性历史）。

## 分支模型

- 功能开发分支：从签出 commit 到最终 commit 的若干领先提交
- 开发分支（CI 合并目标，如 develop/main）：最新 commit
- review 范围 = merge-base(开发分支, HEAD)..HEAD

## 核心概念

| 概念 | 说明 |
|---|---|
| review target | merge-base(开发分支, HEAD)..HEAD；增量模式：上次已 review 的 head..HEAD |
| 任务包 task bundle | 工具生成：综合 diff、hunk/行级变更索引、commit 列表 |
| 意图报告 intent report | agent 经固定接口写入的解释条目集合；工具所有、工具校验 |
| 行级覆盖率 | 变更行全集 ⊆ 报告解释锚定的行集合；核心可靠性机制 |
| 固定接口 | CLI 子命令 + MCP tools 双形态同语义：查询变更事实 / 变更报告 / 校验 |

## 里程碑

- [x] M0 骨架：go module、cmd/yacr、.gitignore、文档就位
- [x] M1 diff 引擎：范围解析、综合 diff 解析、hunk 编号与行级变更索引；fixture repo 测试
- [x] M2 报告存储与接口：report store、CLI 变更接口、校验（行定位/commit/覆盖率）
- [x] M3 TUI（只读）：解释条目浏览、diff+注释渲染、覆盖率视图
- [x] M4 MCP server：与 CLI 同语义的 tools，供会话内 agent 实时查询与写报告
- [x] M5 闭环：增量 review、skill/方法论模板（agent 无关）、反馈摘要生成

## 已定决策

- Go；git 走 shell-out
- TUI：bubbletea / lipgloss，纯只读
- 报告：JSON、工具所有、仅经固定接口（CLI/MCP）变更，每次变更即时校验
- 覆盖粒度：变更行（新增行锚定新侧行号，删除行锚定旧侧行号）
- 解释语言默认中文（由 prompt/skill 约定，校验不管语言）
- 状态目录 `.yacr/`（gitignore）；binary 名 `yacr`
- agent 无关：不内置调起任何 agent；skill 只是可选的方法论文档

## 后续候选（未排期，等真实使用反馈排优先级）

- 旧侧行 blame 归因（cherry-pick/hotfix 免解释过滤的前置）
- commit 过滤（--exclude-author / --exclude-commit，配合归因收缩覆盖宇宙）
- 大 diff：超阈值时 task 警告 + skill 写明按文件迭代的工作法（agent 侧行为，不做默认分片）
- `--include-worktree`（纳入未提交改动的 review）
- TUI 搜索/过滤

## 已解决的设计问题

1. 并发写报告：`.yacr/reports/<id>.json.lock`（flock 排他锁，变更期间持锁）
2. 完成标记：`yacr done`（可 `--force`），记录 reviewed-head 供增量 review
3. rename 定位：new 侧用新路径、old 侧用旧路径；纯重命名用文件级条目
4. 范围解析 v2（ADR-0007）：无启发式，用户确认（--base / config base / 增量），候选按 tip 去重并标注最小领先
5. merge commit：meta 标记 `merge:true`（不列文件），task 输出提示；net diff 天然抵消同步内容
6. MCP 版本协商（回显已知版本）；commit 唯一前缀（≥4 位）；slug 唯一性强制；--range 的 head 必须为 HEAD
