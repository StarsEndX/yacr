---
name: yacr-review
description: 用 yacr 对功能分支领先提交的综合 diff 做意图追踪式 review：逐行写解释、经固定接口写入报告直到行级覆盖完整。仅当用户明确要求用 yacr 审查未推送的提交 / 分支改动，或本 skill 已被加载时使用；不要用于一般性 diff 解释、代码阅读或已合并代码的评审。
---

# YACR 意图追踪 review

把每处变更的意图逐行写进 yacr 报告（JSON，经固定接口写入），供人类用 `yacr view`（只读 TUI）浏览。结论都放进报告条目，不要另写 markdown 评审。

## 心智模型

- **先讲清意图，覆盖率是其次。** `validate` 的 `complete` 用于防遗漏；条目若只是占住行、没讲清"为什么"，即使覆盖了也是坏产出。
- 内容由你定义；工具只校验结构（行定位命中变更行、commit 在范围内、覆盖完整），不评价内容好坏。
- 报告只能经 `yacr report` 或 MCP 工具修改；**不要**手改 `.yacr/` 下任何文件。

## 主循环

1. `yacr task --json` 取任务。若报缺 base：向用户确认"同步源"分支（变更从哪流出，如 `origin/master`；区别于合入目标），由用户 `yacr config base <ref>`（永久）或给你 `--base`（一次性）；**不要替用户猜**。
2. 读任务包：`meta.json`（提交）/ `changes.jsonl`（行级索引）/ `diff.patch`。**大 diff 按文件逐个读**，不要一次吞下整个 patch。
3. 逐处调研意图：读周边代码、`git log -L`、追引用。跨 commit、跨文件的**同一意图合并成一条**；孤立/可疑变更单独成条。
4. `yacr report upsert` 写入解释（示例见下）。可写一条 validate 一条，也可分批。
5. `yacr validate` 循环到 `complete:true`；未解释清单精确到行，按它补齐。
6. `yacr report summary "一句话总评"`。

任务已存在时，仍要先读任务包（第 2 步）再继续。

## 一条好解释

- `title`：这处变更的意图摘要。`explanation` 说清：改了什么、为什么这么改、有什么风险或不确定；默认中文，简洁，别复述 diff 字面。
- 定位：新增行锚 `new` 侧行号、删除行锚 `old` 侧行号；连续一致的改动用 range 合并；binary/纯重命名/权限变更只给 `file`。
- `slug` 是幂等键：同一变更重跑时复用同 slug，会更新原条目。
- 不确定就如实标注（`--tag uncertain` / `--tag needs-user-input`），**不要编造意图**。

```bash
yacr report upsert --slug auth-login \
  --title "新增登录校验" \
  --explanation "login() 校验用户名非空；替换了原来的空实现。" \
  --loc 'app.go:3-9:new' --loc 'app.go:3:old' \
  --commit abc1234 --tag feature
# 批量或复杂定位可用 JSON：yacr report upsert --file entry.json（stdin 用 -）
```

## 查询与接口

- 用户问"某行为什么这么改"：先 `yacr show <file>:<line>` 或 MCP `query_location`，据 hunk + 已有解释作答；未解释则说明并补一条。
- 命令：`yacr task | show | report | validate | done`；全局 `--repo <dir>`、`--target <id>`、`--json`。MCP tools 与 CLI 同语义（`get_task` / `get_diff` / `query_location` / `report_upsert` / `validate` …）。
- 完整参数、EntryInput JSON、全部校验错误码、MCP 配置见 **[reference.md](reference.md)**——需要时再读，不必预先加载。

## 完成后

覆盖完整且写好总评后，提示用户运行 `yacr view` 查看；确认无误后由**用户**执行 `yacr done`（记录 reviewed-head，下次增量 review 只覆盖新提交）。
