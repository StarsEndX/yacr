# yacr 接口参考

按需查阅。核心流程见 [SKILL.md](SKILL.md)。

## 全局

- `--repo <dir>`：目标仓库目录（默认当前目录，向上找 git 顶层）
- `--target <id>`：指定任务 id（默认 `.yacr/current` 指向的任务）
- `--json`：机器可读输出（agent 消费用）
- 报告存于 `.yacr/reports/<target>.json`，由 filelock 保护；CLI 与 MCP 可混用。

## 范围与任务

- review 范围 = `merge-base(base, HEAD)..HEAD`，工作区须干净。范围内若含 merge commit 会被标记 `merge:true` 并降噪提示（工具不阻止；真实合并由 CI 负责）。
- base 无启发式，优先级为：`task --range` > `task --base` > `done` 后的增量（reviewed-head 是 HEAD 祖先时）> `config base` > 拒绝并列出候选。
- 命令：
  - `yacr task [--base <ref> | --range <a>..<b>] [--json]` 生成/刷新任务
  - `yacr config base <ref>` 记忆 / `yacr config base --unset` 清除 / `yacr config get` 查看
  - `yacr done [--force]`：记录 reviewed-head（未覆盖需 `--force`）

## 查询

- `yacr show [--json]`：总览（覆盖率 + 文件列表 + 未解释行）
- `yacr show <file>`：某文件的 hunk 与变更行
- `yacr show <file>[:<line>[-<end>]][:<side>]`：某位置的 hunk、覆盖状态与已有解释
- MCP：`get_task` / `get_files` / `get_commits` / `get_diff {file?}` / `query_location {file,line,side?}`

## 报告写入

```bash
# --loc 可重复；单个定位格式：<file> | <file>:<line>[:<side>] | <file>:<start>-<end>[:<side>]
yacr report upsert \
  --slug <幂等键> | --id <条目id> \
  --title "标题" --explanation "解释" \
  --loc 'app.go:3-9:new' --loc 'app.go:3:old' \
  [--commit <sha前7位或全hash>]... [--tag <tag>]...
yacr report upsert --file entry.json      # 或 --file - 读 stdin
yacr report list [--json]
yacr report delete <id|slug>
yacr report summary "<文本>"              # 无参数则打印当前总评；--file - 可读 stdin
yacr validate [--json]                    # exit 2 = 覆盖不完整
yacr feedback [-o out.md]                 # 导出 markdown 摘要
```

### EntryInput JSON

```json
{
  "id": "e-003",
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

- `id` 与 `slug` 二选一用于更新；都不给则新建。`slug` 在单个任务内唯一。
- `locations[].side`：`new`=新增行锚新侧、`old`=删除行锚旧侧；rename 时 new 侧用新路径、old 侧用旧路径。
- 文件级条目（binary / 纯重命名 / 权限变更）不写 side/start/end，只写 `file`。
- 定位范围可含 hunk 内上下文行，但**必须至少含一行真正变更行**。
- `commits` 必须是范围内 commit（支持前 7 位前缀）。

## 校验错误码

| code | 含义 | 修复 |
|---|---|---|
| `invalid_entry` | title/explanation 为空，或无 locations | 补齐必填字段 |
| `duplicate_slug` | slug 已被其它条目使用 | 换 slug，或用同 slug 更新原条目 |
| `not_found` | 待更新的 id/slug 不存在 | 用 `report list` 核对 |
| `location_unknown_file` | 文件不在 diff 中 | `yacr show` 核对路径；rename 注意新旧路径 |
| `location_no_changed_lines` | 范围只覆盖了上下文行 | 调整 start/end 覆盖真实变更行 |
| `location_range_invalid` | 范围越出 hunk 或 start>end | 拆成多个定位 |
| `location_invalid_side` | side 取值非 new/old | 改正 side |
| `location_side_mismatch` | 路径与 side 不匹配 | 用错误信息给出的正确路径 |
| `location_file_has_hunks` | 有行级变更却只给了文件 | 补 side/start/end |
| `location_not_file_level` | 无行级变更却给了行 | 只给 `file` |
| `commit_unknown` | commit 不在范围内 | 用 `yacr show --json` 查范围内合法 sha |

失败时条目不落盘；`--json` 下返回 `{"error": {"code","message"}}`。

## MCP

tools：`get_task` / `get_files` / `get_commits` / `get_diff` / `query_location` / `report_upsert` / `report_delete` / `report_summary` / `validate`。

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
