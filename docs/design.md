# YACR 设计

## 1. 总体流程（agent 驱动，工具服务）

```
准备:   yacr task [--base <ref>]        → 解析范围、生成任务包（§2-4）

AI 侧（任意 agent，两种通道同语义）:
  CLI:  agent 在会话里 shell 调 yacr 子命令
  MCP:  agent 会话内连接 MCP server（yacr serve 或 stdio）
  ├─ 查询: 读任务包 / diff / hunk 上下文 / 按文件:行查询变更事实
  ├─ 调研: agent 自行读码、git log -L、追引用（工具不管方法论）
  ├─ 写报告: 经固定接口 upsert/delete 解释条目（每次即时校验）
  └─ 问答: 用户在会话内问"某位置变更意图"，agent 用查询接口作答

人类侧:
  yacr view (TUI，只读)  → 解释条目 + diff 渲染 + 覆盖率视图
  yacr validate          → 覆盖率/引用完整性（CI 或收尾用）
```

工具不调起 agent、不定义意图内容；工具的职责 = 变更事实精确化 + 报告接口 + 校验 + 渲染。

## 2. 范围解析

- 场景：功能分支领先开发分支（CI 合并），范围内线性历史，无 merge commit
- base = merge-base(<target_ref>, HEAD)，target_ref 优先级：`--base` 显式指定 > 上次增量 head > upstream `@{u}` > develop/main/master
- head = HEAD
- 工作区必须干净（综合 diff 语义要求）

## 3. 变更模型（行级）

- `git diff --find-renames <base> <head>` 解析为：
  - 文件：`{id: F##, oldPath, newPath, status, hunks[]}`
  - hunk：`{id: "F##.H##", oldStart/oldCount, newStart/newCount, body}`
  - 行级索引：每个 hunk 的变更行展开为 `{side: old|new, lineNo}`（新增行 → 新侧，删除行 → 旧侧），全局可寻址
- 定位格式（对外接口统一）：`{file, side, start, end}`，工具内部映射到 hunk/变更行；校验时拒绝"指向未变更区域"的定位
- 无 hunk 文件（binary/rename/mode-only）以文件级条目参与覆盖

## 4. 任务包 `.yacr/tasks/<base7>..<head7>/`

| 文件 | 内容 |
|---|---|
| meta.json | base/head、branch、commits[]（sha/subject/body/files）、createdAt |
| diff.patch | 原始 patch |
| changes.jsonl | 行级变更索引 + blame 归因提示（该行来自范围内哪个 commit） |

任务包只含事实数据；方法论在 skill/prompt（agent 无关、可选）。

## 5. 报告与固定接口

### 5.1 报告模型（工具所有，仅接口可变）

`.yacr/reports/<base7>..<head7>.json`，带 version、filelock 保护并发：

```json
{
  "version": 1,
  "target_id": "abc1234..def5678",
  "summary": "整体综述（可选）",
  "entries": [
    {
      "id": "e-001",
      "title": "短标题",
      "explanation": "agent 撰写的解释（自由内容，默认中文）",
      "locations": [{"file": "src/x.go", "side": "new", "start": 12, "end": 45}],
      "commits": ["abc1234"],
      "tags": ["可选、自由"],
      "created_at": "...", "updated_at": "..."
    }
  ]
}
```

- id 由工具分配（upsert 幂等：按 id 或唯一 slug）
- locations 必须精确命中变更行；commits 必须存在于范围；违反即拒绝写入
- 多条目覆盖同一行允许，覆盖 = 并集

### 5.2 接口清单（CLI 与 MCP 同语义）

| 操作 | CLI | MCP tool |
|---|---|---|
| 生成任务/取范围 | `yacr task [--base|--range]` | —（任务由 CLI 显式生成） |
| 读任务（meta+覆盖） | `yacr show --json` | `get_task` |
| 文件总览 | `yacr show` | `get_files` / `get_commits` |
| 读 diff/hunk 详情（含每行覆盖状态） | `yacr show <file>` | `get_diff {file}` |
| 按位置查变更事实+已有解释 | `yacr show <file>:<line>[:side]` | `query_location {file,line,side?}` |
| 写/改条目 | `yacr report upsert` | `report_upsert` |
| 删条目 | `yacr report delete <id|slug>` | `report_delete {id_or_slug}` |
| 总评 | `yacr report summary` | `report_summary {text}` |
| 校验与覆盖 | `yacr validate`（exit 2 = 不完整） | `validate` |
| MCP stdio 服务 | `yacr serve` | — |

- 每次 upsert/delete 即时校验并返回剩余未覆盖行；失败返回结构化 ValError（code + 中文 message），条目不落盘
- `--json` 输出供 agent 消费；`.yacr/` 由 filelock 保护并发，CLI 与 MCP 可混用
- 定位格式：`{"file","side":"new|old","start","end"}`；文件级条目只给 `file`；rename 的 new 侧用新路径、old 侧用旧路径

## 6. AI 侧方法论（skill 模板，可选）

1. 取任务包，通读 commits + 综合 diff
2. 自行决定分组粒度：连贯变更合并为一条解释，孤立/可疑变更单独成条
3. 每条：调研意图（读码、git log -L、引用追踪），写明"改了什么、为什么"
4. 循环 upsert → validate，直到覆盖清零
5. 用户实时提问时，用查询接口定位后作答

## 7. TUI（`yacr view`，只读）

- 总览：覆盖率进度、条目列表（按文件分组 + 按 tag 过滤）、未覆盖清单醒目
- 条目详情：explanation + 定位跳转 + 关联 hunk diff
- diff 视图：行级渲染，已覆盖行/未覆盖行着色区分，行上悬浮解释摘要
- 只读：无任何写操作；review 完成标记走 `yacr done`（CLI）

## 8. 闭环

- `yacr done`：标记当前 target review 完成，记录 reviewed-head（branch → head）
- 增量：下次 `yacr task` 检测同分支 reviewed-head 为 HEAD 祖先 → 默认 reviewed-head..HEAD
- 反馈摘要：`yacr feedback` 汇总报告为 markdown（含未覆盖/低置信标签），供用户粘给 agent 或存档

## 9. 边界与策略

- 大 diff（patch > ~1MB）：按文件分批任务；行级索引全局稳定
- binary/submodule：文件级条目
- 对话历史不采集；意图完全由 agent 调研得出
- report 读写均经工具；`.yacr/` 整体 gitignore

## 10. 模块划分

```
cmd/yacr/            task | show | report | validate | view | done | feedback | serve
internal/gitcmd/     git shell-out 封装
internal/diffmodel/  diff 解析、hunk 编号、行级变更索引
internal/taskgen/    任务包生成
internal/report/     报告存储、固定接口语义、校验、覆盖率
internal/session/    reviewed-head / 完成标记
internal/tui/        bubbletea（只读）
internal/mcpserver/  MCP server（tools 与 CLI 同语义）
skills/              方法论模板（agent 无关）
testdata/            fixture repos / golden files
```
