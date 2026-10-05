# AGENTS.md — YACR 开发约定（assistant 维护，用户不管理此文件）

## 项目

对功能分支领先 commit 的综合 diff 做"意图追踪式" AI code review 的 Go 工具。agent 无关：工具供 agent 经固定接口（CLI/MCP）使用；人类 TUI 只读。目标见 `TODO.md`，详细设计见 `docs/design.md`（以 design.md 为 single source of design truth）。

## 命令

- 构建：`go build ./...`
- 测试：`go test ./...`
- 静态检查：`go vet ./...`
- 二进制：`go build -o bin/yacr ./cmd/yacr`

## 架构速览

分层：`gitcmd`（git shell-out）→ `diffmodel`（diff 解析/hunk 编号/行级变更索引）→ `taskgen`（任务包）→ `report`（存储/接口/校验/覆盖率）→ `session`（reviewed-head）→ `tui`（只读）/ `mcpserver`。

关键不变量：
- 行级覆盖是核心可靠性机制：变更行全集必须被报告解释覆盖
- 意图内容由 agent 定义，工具只校验结构（行定位命中变更行、commit 存在、覆盖完整）
- 报告为工具所有，只能经固定接口（CLI/MCP 同语义）变更，每次变更即时校验
- 报告不依赖 agent 的文件编辑能力
- TUI 纯只读
- 范围内无 merge commit（CI 负责真实合并），hunk/行 ID 在任务内全局稳定，分批不得重编号

## 约定

- 不写注释，除非用户要求
- 错误处理：wrap with `%w`；对外 CLI 错误信息人类可读
- 标识符英文，文档中文；解释内容默认中文（由 skill/prompt 约定）
- 测试优先用 `t.TempDir()` + 真实 git 命令构造 fixture（gitcmd 已封装），diff 解析用 testdata golden files
- commit message：conventional commits；不主动 commit/push
- UI 文案中文

## 设计决策记录

- ADR-0001 git 走 shell-out 而非 go-git：rename 检测、merge-base、blame 语义更可靠，fixture 测试也依赖真实 git
- ADR-0002 报告为结构化 JSON（非 markdown）：TUI 需要 commit hash + 行锚定做组合渲染
- ADR-0003 agent 无关：工具不调起 agent，只提供 CLI/MCP 固定接口；skill 仅是可选方法论文档
- ADR-0004 工作区不干净时拒绝 review（综合 diff 语义要求）
- ADR-0005 报告固定接口：每次变更即时校验（行定位/commit 存在性/覆盖率），拒绝非法写入，不依赖 agent 编辑文件
- ADR-0006 覆盖粒度为变更行：新增行锚定新侧、删除行锚定旧侧；连贯变更可合并为一条解释
- ADR-0007 范围解析无启发式：base 必须用户确认（--base 一次性 / yacr config base 永久 / done 后增量延续）；upstream 仅作候选展示，不作默认——同步源与合入目标是两个角色，猜测必错（feature 可能已含同步源 hotfix）

## 当前状态

- M0-M5 全部完成：CLI（task/show/report/validate/view/done/feedback/serve/config）、只读 TUI、MCP server、增量 review、skill 文档（`skills/yacr-review/SKILL.md`）
- 范围解析 v2（ADR-0007 无启发式 + yacr config base）与 agent-usable 硬化已完成
- 后续候选见 TODO.md「后续候选」（等真实使用反馈排优先级）

## 自测试方案（安全、可复现、不越界）

- 全部测试基于 `t.TempDir()` + 真实 git 构造 fixture（`internal/fixture`），自动清理，不触碰真实仓库与全局 git 配置（身份经 `-c user.*` 与 env 注入）
- gitcmd/diffmodel/taskgen/report/session/mcpserver 各有单元测试；`cmd/yacr` 有二进制级 e2e（TestMain 构建 temp 二进制后驱动完整流程：task → upsert → validate → done → 增量 → MCP）
- 无网络依赖；`go test ./...` 一条命令可复现全部
