# YACR — Yet Another Code Reviewer

对功能分支领先 commit 的**综合 diff** 做"意图追踪式" AI code review 的 Go 工具。agent 无关：工具供任意 agent 经固定接口（CLI / MCP）使用；人类使用只读 TUI 浏览结果。

A Go tool for intent-tracing AI code review on the **combined diff** of a feature branch's ahead commits. Agent-agnostic: the tool serves any agent via fixed interfaces (CLI / MCP); humans browse results in a read-only TUI.

## 中文

### 问题

与 coding agent 协作时，多轮任务累积出多个未 push 的领先 commit。到临界点发起 review 时：

- 逐 commit 看没有意义，真正需要 review 的是**领先提交的综合 diff**
- agent 中途的顺带改动（要求改 A1，却混入了 A2、A3）跨 commit 后很难被发现
- 在 agent 会话里直接看 diff 难看懂，可靠性也难保证

### 核心机制

- **行级覆盖保证**：每一处变更行都必须有解释锚定（`validate` 强制）；连贯变更可合并为一条解释
- **意图由 agent 定义**：工具只精确暴露变更事实（diff / 行定位 / commit），不规定"什么是意图"
- **固定报告接口**：报告为工具所有（JSON），只能经 CLI / MCP 变更，每次写入即时校验（行定位、commit 存在性、覆盖率）
- **agent 无关**：不内置调起任何 agent；CLI 子命令与 MCP tools 同语义
- **人类只读**：TUI（`yacr view`）渲染解释 + diff + 覆盖率，不提供编辑
- **增量 review**：`yacr done` 记录 reviewed-head，下次从上次完成处延续

### 范围

review 范围 = `merge-base(base, HEAD)..HEAD`。base 必须是用户确认过的事实，无启发式猜测：`--base` 一次性指定 / `yacr config base` 永久记忆 / `done` 后自动增量。工作区必须干净（综合 diff 语义要求）；范围内不含 merge commit。

### 构建

需要 Go 1.26+ 与 git（shell-out 调用，依赖真实 git 语义）。

```sh
make build   # 产出 bin/yacr
make test    # go test ./...
```

### 快速上手

```sh
yacr task --base origin/master    # 生成任务包（解析范围 + 综合 diff + 行级变更索引）
# … agent 经 CLI / MCP 读任务、调研代码、经 report 接口写解释 …
yacr validate                     # 校验覆盖率与引用完整性（不完整时 exit 2）
yacr view                         # 人类只读 TUI 浏览
yacr done                         # 标记完成，记录 reviewed-head 供增量
yacr feedback                     # 导出 markdown 摘要
```

agent 会话内可运行 `yacr serve`（MCP，stdio），tools 与 CLI 同语义；随时用 `yacr show <file>:<line>` 回答"这里为什么这么改"。

### 命令

| 命令 | 作用 |
|---|---|
| `yacr task [--base \| --range]` | 解析范围并生成任务包 |
| `yacr show [<file>[:<line>][:side]]` | 查询总览 / 文件 / 行级变更事实与已有解释 |
| `yacr report list \| upsert \| delete \| summary` | 报告固定变更接口（即时校验） |
| `yacr validate` | 校验覆盖率与引用完整性 |
| `yacr view` | 人类只读 TUI |
| `yacr done [--force]` | 标记完成并记录 reviewed-head |
| `yacr feedback` | 导出 markdown 摘要 |
| `yacr serve` | MCP server（stdio） |
| `yacr config base <ref>` | 记忆 / 清除 / 查看 base |

全局：`--repo <dir>`、`--target <id>`、`--json`（机器可读输出）。

### 文档

- 设计（single source of design truth）：[docs/design.md](docs/design.md)
- 路线图与背景：[TODO.md](TODO.md)
- 开发约定：[AGENTS.md](AGENTS.md)
- agent 方法论模板（可选）：[skills/yacr-review/SKILL.md](skills/yacr-review/SKILL.md)

## English

### Problem

When working with a coding agent, multiple rounds of tasks accumulate unpushed ahead commits. When review is finally requested:

- Reviewing per-commit is meaningless; what needs review is the **combined diff** of the ahead commits
- The agent's incidental changes (asked to fix A1, but A2 and A3 got mixed in) are hard to spot across commits
- Reading raw diffs inside an agent session is hard to follow, and reliability is hard to guarantee

### Core mechanics

- **Line-level coverage guarantee**: every changed line must be anchored by an explanation (enforced by `validate`); coherent changes may share one explanation
- **Intent is defined by the agent**: the tool only exposes change facts precisely (diff / line locations / commits); it does not define "intent"
- **Fixed report interface**: reports are tool-owned (JSON) and mutable only via CLI / MCP, validated on every write (line locations, commit existence, coverage)
- **Agent-agnostic**: no built-in agent invocation; CLI subcommands and MCP tools are semantically identical
- **Humans are read-only**: the TUI (`yacr view`) renders explanations + diff + coverage with no editing
- **Incremental review**: `yacr done` records the reviewed head; the next run resumes from there

### Scope

Review range = `merge-base(base, HEAD)..HEAD`. The base must be a user-confirmed fact — no heuristic guessing: `--base` (one-shot) / `yacr config base` (persistent) / automatic increment after `done`. The working tree must be clean (required by combined-diff semantics); the range contains no merge commits.

### Build

Requires Go 1.26+ and git (invoked via shell-out, relying on real git semantics).

```sh
make build   # produces bin/yacr
make test    # go test ./...
```

### Quick start

```sh
yacr task --base origin/master    # generate the task bundle (scope + combined diff + line-level change index)
# … the agent reads the task via CLI / MCP, investigates code, writes explanations via the report interface …
yacr validate                     # validate coverage and references (exit 2 if incomplete)
yacr view                         # read-only TUI for humans
yacr done                         # mark complete, record reviewed head for incremental review
yacr feedback                     # export a markdown summary
```

Inside an agent session, run `yacr serve` (MCP over stdio); the tools mirror the CLI semantics, and `yacr show <file>:<line>` answers "why was this changed" at any time.

### Commands

| Command | Purpose |
|---|---|
| `yacr task [--base \| --range]` | Resolve scope and generate the task bundle |
| `yacr show [<file>[:<line>][:side]]` | Query overview / file / per-line change facts and existing explanations |
| `yacr report list \| upsert \| delete \| summary` | Fixed report mutation interface (validated immediately) |
| `yacr validate` | Validate coverage and reference integrity |
| `yacr view` | Read-only TUI for humans |
| `yacr done [--force]` | Mark complete and record the reviewed head |
| `yacr feedback` | Export a markdown summary |
| `yacr serve` | MCP server (stdio) |
| `yacr config base <ref>` | Remember / clear / show the base |

Global flags: `--repo <dir>`, `--target <id>`, `--json` (machine-readable output).

### Documentation

- Design (single source of design truth): [docs/design.md](docs/design.md)
- Roadmap and background: [TODO.md](TODO.md)
- Development conventions: [AGENTS.md](AGENTS.md)
- Agent methodology template (optional): [skills/yacr-review/SKILL.md](skills/yacr-review/SKILL.md)
