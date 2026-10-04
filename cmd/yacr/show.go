package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"yacr/internal/app"
	"yacr/internal/gitcmd"
	"yacr/internal/session"
	"yacr/internal/taskgen"
)

func cmdTask(args []string) (error, int) {
	fs := flag.NewFlagSet("task", flag.ContinueOnError)
	fs.Usage = func() {}
	baseRef := fs.String("base", "", "base 引用（取其与 HEAD 的 merge-base）")
	rng := fs.String("range", "", "显式范围 <base>..<head>")
	var g globalFlags
	rest := parseGlobal(&g, args)
	if err := fs.Parse(rest); err != nil {
		return fmt.Errorf("参数错误: %w", err), 1
	}

	dir := g.repo
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err, 1
		}
		dir = cwd
	}
	repoDir, err := app.FindRepoDir(dir)
	if err != nil {
		return err, 1
	}
	yacrDir := filepath.Join(repoDir, ".yacr")
	repo := gitcmd.New(repoDir)

	lastReviewed := func(branch string) (string, bool) {
		return session.LastReviewedHead(yacrDir, branch)
	}
	res, err := taskgen.Generate(repo, yacrDir, taskgen.Options{
		ExplicitBase: *baseRef,
		Range:        *rng,
		LastReviewed: lastReviewed,
	})
	if err != nil {
		return err, 1
	}

	if g.json {
		body := map[string]any{
			"target_id":   res.Meta.TargetID,
			"base":        res.Meta.Base,
			"head":        res.Meta.Head,
			"branch":      res.Meta.Branch,
			"base_ref":    res.Meta.BaseRef,
			"incremental": res.Meta.Incremental,
			"commits":     res.Meta.Commits,
			"stats":       res.Stats,
			"task_dir":    res.Dir,
			"next_steps": []string{
				"读取任务包: " + res.Dir + " 下的 meta.json / changes.jsonl / diff.patch",
				"调研每处变更的意图，然后通过 `yacr report upsert` 写入解释条目",
				"运行 `yacr validate` 检查覆盖率，直到全部变更被解释",
				"人类通过 `yacr view` 查看",
			},
		}
		if err := printJSON(body); err != nil {
			return err, 1
		}
		return nil, 0
	}

	fmt.Printf("review 任务已生成: %s\n", res.Meta.TargetID)
	fmt.Printf("  范围: %s..%s（分支 %s, base 来源: %s%s）\n", gitcmd.Short(res.Meta.Base), gitcmd.Short(res.Meta.Head), res.Meta.Branch, res.Meta.BaseRef, incrementalMark(res.Meta.Incremental))
	fmt.Printf("  提交: %d 个, 文件: %d, hunk: %d, 变更行: %d, 文件级条目: %d\n",
		res.Stats.Commits, res.Stats.Files, res.Stats.Hunks, res.Stats.ChangedLines, res.Stats.FileUnits)
	fmt.Printf("  任务包: %s\n", res.Dir)
	fmt.Println()
	fmt.Println("下一步（交给 agent）:")
	fmt.Println("  1. 读取任务包 meta.json / changes.jsonl / diff.patch")
	fmt.Println("  2. 调研每处变更的意图，`yacr report upsert` 写入解释（即时校验）")
	fmt.Println("  3. `yacr validate` 直到覆盖完整；人类 `yacr view` 查看")
	return nil, 0
}

func incrementalMark(inc bool) string {
	if inc {
		return "，增量"
	}
	return ""
}

func cmdShow(args []string) (error, int) {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.Usage = func() {}
	var g globalFlags
	rest := parseGlobal(&g, args)
	if err := fs.Parse(rest); err != nil {
		return fmt.Errorf("参数错误: %w", err), 1
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("show 只接受一个参数: <file>[:<line>[-<end>]][:<side>]"), 1
	}
	ctx, err := loadCtx(g, "")
	if err != nil {
		return err, 1
	}

	if fs.NArg() == 0 {
		return showOverview(ctx, g)
	}
	spec := fs.Arg(0)
	file, q, ok := parseShowSpec(spec)
	if !ok {
		return fmt.Errorf("参数格式: <file>[:<line>[-<end>]][:<side>]，收到 %q", spec), 1
	}
	if q == nil {
		return showFile(ctx, g, file)
	}
	q.File = file
	return showLocation(ctx, g, *q)
}

func parseShowSpec(spec string) (string, *app.LocationQuery, bool) {
	parts := strings.Split(spec, ":")
	if len(parts) > 3 {
		return "", nil, false
	}
	file := parts[0]
	if file == "" {
		return "", nil, false
	}
	if len(parts) == 1 {
		return file, nil, true
	}
	q := &app.LocationQuery{}
	if err := parseLineSpec(parts[1], q); err != nil {
		return "", nil, false
	}
	if len(parts) == 3 {
		q.Side = parts[2]
	}
	return file, q, true
}

func parseLineSpec(s string, q *app.LocationQuery) error {
	if s == "" {
		return fmt.Errorf("空行号")
	}
	dash := strings.IndexByte(s, '-')
	if dash < 0 {
		n := 0
		if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n <= 0 {
			return fmt.Errorf("行号错误: %q", s)
		}
		q.Line, q.End = n, n
		return nil
	}
	lo, hi := s[:dash], s[dash+1:]
	l, e := 0, 0
	if _, err := fmt.Sscanf(lo, "%d", &l); err != nil || l <= 0 {
		return fmt.Errorf("行号错误: %q", s)
	}
	if _, err := fmt.Sscanf(hi, "%d", &e); err != nil || e < l {
		return fmt.Errorf("行号错误: %q", s)
	}
	q.Line, q.End = l, e
	return nil
}

func showOverview(ctx *app.Ctx, g globalFlags) (error, int) {
	cov, files := ctx.Overview()
	if g.json {
		body := map[string]any{
			"target_id":   ctx.TargetID(),
			"coverage":    cov,
			"files":       files,
			"summary":     ctx.Summary(),
			"entry_count": len(ctx.Entries()),
		}
		if err := printJSON(body); err != nil {
			return err, 1
		}
		return nil, 0
	}
	fmt.Printf("target %s  base %s..%s\n", ctx.TargetID(), gitcmd.Short(ctx.Meta.Base), gitcmd.Short(ctx.Meta.Head))
	fmt.Printf("覆盖率: %d/%d 变更行", cov.CoveredLines, cov.TotalLines)
	if cov.FileUnitsTotal > 0 {
		fmt.Printf(", %d/%d 文件级条目", cov.FileUnitsCovered, cov.FileUnitsTotal)
	}
	fmt.Println()
	if !cov.Complete {
		for _, u := range cov.Uncovered {
			fmt.Printf("  未解释: %s:%s  行 %s\n", u.File, u.Side, joinInts(u.Lines))
		}
		for _, f := range cov.UncoveredFiles {
			fmt.Printf("  未解释: %s（文件级）\n", f)
		}
	}
	fmt.Println()
	for _, f := range files {
		mark := " "
		if f.FileUnit {
			if f.FileUnitCovered {
				mark = "✓"
			} else {
				mark = "!"
			}
		} else if len(f.UncoveredNew) == 0 && len(f.UncoveredOld) == 0 {
			mark = "✓"
		} else {
			mark = "!"
		}
		path := f.Path
		if f.OldPath != "" {
			path = f.OldPath + " -> " + f.Path
		}
		fmt.Printf("%s %s %-9s %s hunks:%d 变更行:+%d/-%d\n", mark, f.ID, path, f.Status, f.Hunks, f.ChangedNew, f.ChangedOld)
	}
	fmt.Println()
	fmt.Printf("解释条目: %d 个（`yacr show <file>:<line>` 查看行级详情）\n", len(ctx.Entries()))
	return nil, 0
}

func showFile(ctx *app.Ctx, g globalFlags, file string) (error, int) {
	f := ctx.Model.FileByPath(file, "new")
	if f == nil {
		f = ctx.Model.FileByPath(file, "old")
	}
	if f == nil {
		return fmt.Errorf("文件 %s 不在本次 diff 中", file), 1
	}
	if g.json {
		body := map[string]any{
			"id": f.ID, "old_path": f.OldPath, "new_path": f.NewPath,
			"status": string(f.Status), "binary": f.Binary, "hunks": f.Hunks,
		}
		if err := printJSON(body); err != nil {
			return err, 1
		}
		return nil, 0
	}
	fmt.Printf("%s %s (%s)\n", f.ID, f.Path(), f.Status)
	for _, h := range f.Hunks {
		fmt.Printf("  %s  -%d,%d +%d,%d  变更新侧行: %s / 旧侧行: %s\n",
			h.ID, h.OldStart, h.OldCount, h.NewStart, h.NewCount,
			joinInts(h.ChangedLines("new")), joinInts(h.ChangedLines("old")))
	}
	return nil, 0
}

func showLocation(ctx *app.Ctx, g globalFlags, q app.LocationQuery) (error, int) {
	res, err := ctx.Query(q)
	if err != nil {
		return err, 1
	}
	if g.json {
		if err := printJSON(res); err != nil {
			return err, 1
		}
		return nil, 0
	}
	if found, _ := res["found"].(bool); !found {
		fmt.Printf("%s: 该行不在变更区域内\n", q.File)
		return nil, 0
	}
	file, _ := res["file"].(string)
	side, _ := res["side"].(string)
	hunkID, _ := res["hunk"].(string)
	changed, _ := res["changed"].(bool)
	entries, _ := res["entries"].([]map[string]any)
	fmt.Printf("%s:%d (%s 侧, %s)\n", file, q.Line, side, hunkID)
	if !changed {
		fmt.Println("  该行是上下文行（未变更）")
	}
	if len(entries) == 0 {
		fmt.Println("  尚无解释覆盖该行（未解释）")
	} else {
		for _, e := range entries {
			id, _ := e["id"].(string)
			title, _ := e["title"].(string)
			expl, _ := e["explanation"].(string)
			fmt.Printf("  [%s] %s\n", id, title)
			for _, ln := range strings.Split(strings.TrimRight(expl, "\n"), "\n") {
				fmt.Printf("    %s\n", ln)
			}
		}
	}
	hv, err := ctx.HunkView(hunkID)
	if err == nil {
		fmt.Println()
		renderHunk(hv)
	}
	return nil, 0
}

func renderHunk(hv *app.HunkView) {
	fmt.Printf("  %s  @@ -%d,%d +%d,%d @@\n", hv.ID, hv.OldStart, hv.OldCount, hv.NewStart, hv.NewCount)
	for _, ln := range hv.Lines {
		mark := " "
		switch {
		case ln.Changed && ln.Covered:
			mark = "●"
		case ln.Changed:
			mark = "○"
		}
		prefix := " "
		if ln.Type == "add" {
			prefix = "+"
		} else if ln.Type == "del" {
			prefix = "-"
		}
		fmt.Printf("  %s %s %4d %4d %s\n", mark, prefix, ln.OldNo, ln.NewNo, truncateRunes(ln.Text, 100))
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func joinInts(a []int) string {
	parts := make([]string, 0, len(a))
	for _, n := range a {
		parts = append(parts, fmt.Sprintf("%d", n))
	}
	return strings.Join(parts, ",")
}
