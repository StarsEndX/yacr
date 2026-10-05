---
name: yacr-review
description: 使用 yacr 工具做"意图追踪式" code review：对功能分支领先提交的综合 diff 逐行追踪意图，经固定接口（CLI/MCP）写入解释报告直到覆盖完整。当用户要求 review 未推送的提交、审查分支改动、解释 diff 时使用。
---

# YACR 意图追踪 Review

对**功能分支领先开发分支的全部 commit 的综合 diff**做 review。你的产出不是一份 markdown，而是一组经 yacr 固定接口写入、逐行锚定的解释条目。人类通过 `yacr view`（只读 TUI）查看。

## 核心约束（必须遵守）

1. **报告只能经固定接口变更**：`yacr report ...` 子命令或 MCP tools。禁止直接编辑 `.yacr/` 下任何文件。
2. **每一行变更都必须被解释**：新增行锚定 new 侧行号、删除行锚定 old 侧行号；连续一致的变更可合并为一条条目（range 定位）；binary/纯重命名/权限变更用文件级定位（只给 file）。
3. **每次写入即时校验**：行定位必须命中变更行、commit hash 必须在范围内、结构必须完整。校验失败的条目不会落盘。
4. 意图内容由你定义：说清"改了什么、为什么、有什么风险/疑问"，默认中文。不确定就如实标注（tags 如 `uncertain`、`needs-user-input`）。

## 流程

```
1. yacr task --json            # 若任务已生成则跳过；得到 target_id、变更统计、任务包路径
   # 注意：task 无确认 base 时会拒绝并列出候选分支——这是设计行为。
   # 此时向用户确认"同步源"分支（变更从哪里流出，如 origin/master，不是合入目标），
   # 然后由用户执行 yacr config base <ref>（永久）或告诉你 --base（一次性）。不要替用户猜。
2. 读取任务包                   # meta.json（提交列表）/ changes.jsonl（行级索引+blame归因）/ diff.patch
3. 调研每处变更的意图            # 读周边代码、git log -L、追引用；跨 commit 的同一意图合并为一个条目
4. yacr report upsert ...      # 分批写入解释（见下方格式）
5. yacr validate               # 循环直到 complete=true；未解释清单会精确到行
6. yacr report summary "总评"   # 一句话概括本次变更全貌
```

用户说"review 我的提交"→ 从 1 开始；任务已存在 → 从 3 开始。

## CLI 速查

```bash
yacr task [--base <ref> | --range <a>..<b>] [--json]   # 生成/刷新任务
yacr show [--json]                                     # 总览：覆盖率 + 文件列表 + 未解释行
yacr show <file>[:<line>[-<end>]][:<side>]             # 查某文件/某行的变更事实与已有解释
yacr report upsert --slug <幂等键> \
    --title "标题" --explanation "解释" \
    --loc '<file>:<start>-<end>:<side>' \              # --loc 可重复
    [--commit <sha前7位>]... [--tag <tag>]...           # --commit/--tag 可重复
yacr report upsert --file entry.json                   # 或提交 EntryInput JSON（stdin 用 -）
yacr report delete <id|slug>
yacr report summary "<总评文本>"
yacr validate [--json]                                 # exit 2 = 覆盖不完整
# 所有命令支持 --repo <dir> 与 --target <任务id>（默认 .yacr/current 指向的任务）
yacr feedback [-o out.md]                              # 导出 markdown 摘要
```

## EntryInput JSON

```json
{
  "slug": "auth-login",
  "title": "新增登录校验",
  "explanation": "login() 校验用户名非空；extra() 演示接入。替换了原来的空实现。",
  "locations": [
    {"file": "auth.go", "side": "new", "start": 3, "end": 8},
    {"file": "app.go", "side": "old", "start": 12, "end": 12},
    {"file": "logo.bin"}
  ],
  "commits": ["abc1234"],
  "tags": ["feature"]
}
```

- `locations[].side`：`new`=新侧行号（新增行），`old`=旧侧行号（删除行）；rename 场景 new 侧用新路径、old 侧用旧路径
- 文件级条目（binary/纯重命名/权限变更）不写 side/start/end，只写 file
- 定位范围可以覆盖 hunk 内的上下文行，但必须至少含一行真正的变更行
- `slug` 是幂等更新键：重跑 review 时同 slug 更新而非新建

## MCP 通道（会话内使用）

tools 与 CLI 同语义：`get_task` / `get_files` / `get_commits` / `get_diff` / `query_location` / `report_upsert` / `report_delete` / `report_summary` / `validate`。

服务启动（stdio）：

```bash
yacr serve --repo /path/to/repo
```

opencode（`.opencode/opencode.json`）：

```json
{ "mcp": { "yacr": { "type": "local", "command": ["yacr", "serve"], "enabled": true } } }
```

Claude Code（`.mcp.json`）：

```json
{ "mcpServers": { "yacr": { "command": "yacr", "args": ["serve"] } } }
```

MCP 与 CLI 共享同一报告文件（filelock 保护），可混用。

## 回答用户实时提问

用户问"X 文件某行为什么这么改"时：先 `query_location`（或 `yacr show <file>:<line>`）拿到 hunk、已有解释与覆盖状态，结合周边代码作答；若该行未解释，说明并补一条解释。

## 常见校验错误与修复

| code | 含义 | 修复 |
|---|---|---|
| `location_unknown_file` | 文件不在 diff 中 | 用 `yacr show` 核对路径；rename 注意新旧路径 |
| `location_no_changed_lines` | 范围只覆盖了上下文行 | 调整 start/end 覆盖真实变更行 |
| `location_range_invalid` | 范围越出 hunk | 拆成多个定位 |
| `location_side_mismatch` | 路径与 side 不匹配 | 用错误信息里给出的正确路径 |
| `location_file_has_hunks` | 有行级变更却只给了文件 | 补 side/start/end |
| `location_not_file_level` | 无行级变更却给了行 | 只给 file |
| `commit_unknown` | commit 不在范围内 | 用 `yacr show --json` 查范围内合法 sha |

## 完成标准

`yacr validate` exit 0（complete=true），并已设置总评。之后提示用户运行 `yacr view` 查看；用户确认无误后由用户执行 `yacr done`（工具会记录 reviewed-head，下次增量 review 只覆盖新提交）。
