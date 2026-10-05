package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"time"
	"yacr/internal/gitcmd"
	"yacr/internal/report"
	"yacr/internal/session"
)

func cmdReport(args []string) (error, int) {
	if len(args) == 0 {
		return fmt.Errorf("用法: yacr report <list|upsert|delete|summary>"), 1
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return cmdReportList(rest)
	case "upsert":
		return cmdReportUpsert(rest)
	case "delete":
		return cmdReportDelete(rest)
	case "summary":
		return cmdReportSummary(rest)
	default:
		return fmt.Errorf("未知子命令 report %q", sub), 1
	}
}

func cmdReportList(args []string) (error, int) {
	var g globalFlags
	args = parseGlobal(&g, args)
	fs := flag.NewFlagSet("report list", flag.ContinueOnError)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("参数错误: %w", err), 1
	}
	ctx, err := loadCtx(g, g.target)
	if err != nil {
		return err, 1
	}
	entries := ctx.Entries()
	if g.json {
		if err := printJSON(map[string]any{
			"target_id": ctx.TargetID(),
			"summary":   ctx.Summary(),
			"entries":   entries,
		}); err != nil {
			return err, 1
		}
		return nil, 0
	}
	if s := ctx.Summary(); s != "" {
		fmt.Printf("总评: %s\n\n", s)
	}
	if len(entries) == 0 {
		fmt.Println("（暂无解释条目）")
		return nil, 0
	}
	for _, e := range entries {
		fmt.Printf("[%s] %s\n", e.ID, e.Title)
		for _, loc := range e.Locations {
			fmt.Printf("    %s\n", loc.String())
		}
		if len(e.Commits) > 0 {
			fmt.Printf("    commits: %s\n", shortCommits(e.Commits))
		}
		if len(e.Tags) > 0 {
			fmt.Printf("    tags: %s\n", strings.Join(e.Tags, ", "))
		}
		fmt.Printf("    %s\n", oneLine(e.Explanation))
	}
	return nil, 0
}

func cmdReportUpsert(args []string) (error, int) {
	fs := flag.NewFlagSet("report upsert", flag.ContinueOnError)
	fs.Usage = func() {}
	file := fs.String("file", "", "EntryInput JSON 文件路径（- 表示 stdin）")
	idFlag := fs.String("id", "", "更新指定 id 的条目")
	slugFlag := fs.String("slug", "", "条目 slug（幂等更新键）")
	title := fs.String("title", "", "标题")
	explanation := fs.String("explanation", "", "解释内容")
	commitFlags := repeated(fs, "commit", "关联 commit（可重复，范围前 7 位或全 hash）")
	tagFlags := repeated(fs, "tag", "标签（可重复）")
	locFlags := repeated(fs, "loc", "定位（可重复）: <file> | <file>:<line>[:<side>] | <file>:<start>-<end>[:<side>]")
	var g globalFlags
	rest := parseGlobal(&g, args)
	if err := fs.Parse(rest); err != nil {
		return fmt.Errorf("参数错误: %w", err), 1
	}

	var in report.EntryInput
	if *file != "" {
		var data []byte
		var err error
		if *file == "-" {
			data, err = io.ReadAll(os.Stdin)
		} else {
			data, err = os.ReadFile(*file)
		}
		if err != nil {
			return fmt.Errorf("读取输入失败: %w", err), 1
		}
		if err := json.Unmarshal(data, &in); err != nil {
			return fmt.Errorf("EntryInput JSON 解析失败: %w", err), 1
		}
	} else {
		in = report.EntryInput{
			ID:          *idFlag,
			Slug:        *slugFlag,
			Title:       *title,
			Explanation: *explanation,
			Commits:     *commitFlags,
			Tags:        *tagFlags,
		}
		for _, spec := range *locFlags {
			loc, err := parseLocSpec(spec)
			if err != nil {
				return err, 1
			}
			in.Locations = append(in.Locations, loc)
		}
	}

	ctx, err := loadCtx(g, g.target)
	if err != nil {
		return err, 1
	}
	res, verr, err := ctx.Service.Upsert(ctx.TargetID(), in)
	if err != nil {
		return err, 1
	}
	if verr != nil {
		if g.json {
			if err := printJSON(map[string]any{"error": verr}); err != nil {
				return err, 1
			}
		} else {
			fmt.Fprintf(os.Stderr, "校验失败: %s\n", verr.Error())
		}
		return verr, 2
	}
	if g.json {
		if err := printJSON(res); err != nil {
			return err, 1
		}
		return nil, 0
	}
	cov := res.Coverage
	fmt.Printf("已保存条目 %s（%s）\n", res.Entry.ID, res.Entry.Title)
	for _, loc := range res.Entry.Locations {
		fmt.Printf("  %s\n", loc.String())
	}
	fmt.Printf("覆盖率: %d/%d 变更行", cov.CoveredLines, cov.TotalLines)
	if cov.FileUnitsTotal > 0 {
		fmt.Printf(", %d/%d 文件级", cov.FileUnitsCovered, cov.FileUnitsTotal)
	}
	if cov.Complete {
		fmt.Println(" — 全部覆盖 ✓")
	} else {
		fmt.Println()
		for _, u := range cov.Uncovered {
			fmt.Printf("  未解释: %s:%s 行 %s\n", u.File, u.Side, joinInts(u.Lines))
		}
		for _, f := range cov.UncoveredFiles {
			fmt.Printf("  未解释: %s（文件级）\n", f)
		}
	}
	return nil, 0
}

func parseLocSpec(spec string) (report.Location, error) {
	parts := strings.Split(spec, ":")
	if len(parts) > 3 || parts[0] == "" {
		return report.Location{}, fmt.Errorf("--loc 格式: <file> | <file>:<line>[:<side>] | <file>:<start>-<end>[:<side>]，收到 %q", spec)
	}
	loc := report.Location{File: parts[0]}
	if len(parts) == 1 {
		return loc, nil
	}
	lo, side := parts[1], ""
	if len(parts) == 3 {
		side = parts[2]
	}
	if side != "" && side != "new" && side != "old" {
		return report.Location{}, fmt.Errorf("--loc side 必须是 new/old: %q", spec)
	}
	loc.Side = side
	dash := strings.IndexByte(lo, '-')
	if dash < 0 {
		n := 0
		if _, err := fmt.Sscanf(lo, "%d", &n); err != nil || n <= 0 {
			return report.Location{}, fmt.Errorf("--loc 行号错误: %q", spec)
		}
		loc.Start, loc.End = n, n
	} else {
		l, e := 0, 0
		if _, err := fmt.Sscanf(lo[:dash], "%d", &l); err != nil || l <= 0 {
			return report.Location{}, fmt.Errorf("--loc 行号错误: %q", spec)
		}
		if _, err := fmt.Sscanf(lo[dash+1:], "%d", &e); err != nil || e < l {
			return report.Location{}, fmt.Errorf("--loc 行号错误: %q", spec)
		}
		loc.Start, loc.End = l, e

	}
	return loc, nil
}

func cmdReportDelete(args []string) (error, int) {
	fs := flag.NewFlagSet("report delete", flag.ContinueOnError)
	fs.Usage = func() {}
	var g globalFlags
	rest := parseGlobal(&g, args)
	if err := fs.Parse(rest); err != nil || fs.NArg() != 1 {
		return fmt.Errorf("用法: yacr report delete <id|slug>"), 1
	}
	ctx, err := loadCtx(g, g.target)
	if err != nil {
		return err, 1
	}
	cov, verr, err := ctx.Service.Delete(ctx.TargetID(), fs.Arg(0))
	if err != nil {
		return err, 1
	}
	if verr != nil {
		if g.json {
			_ = printJSON(map[string]any{"error": verr})
		} else {
			fmt.Fprintf(os.Stderr, "删除失败: %s\n", verr.Error())
		}
		return verr, 2
	}
	if g.json {
		_ = printJSON(map[string]any{"deleted": true, "coverage": cov})
		return nil, 0
	}
	fmt.Printf("已删除。覆盖率: %d/%d 变更行\n", cov.CoveredLines, cov.TotalLines)
	return nil, 0
}

func cmdReportSummary(args []string) (error, int) {
	fs := flag.NewFlagSet("report summary", flag.ContinueOnError)
	fs.Usage = func() {}
	file := fs.String("file", "", "从文件读取总评（- 为 stdin）")
	var g globalFlags
	rest := parseGlobal(&g, args)
	if err := fs.Parse(rest); err != nil {
		return fmt.Errorf("参数错误: %w", err), 1
	}
	ctx, err := loadCtx(g, g.target)
	if err != nil {
		return err, 1
	}
	var text string
	if *file != "" {
		var data []byte
		if *file == "-" {
			data, err = io.ReadAll(os.Stdin)
		} else {
			data, err = os.ReadFile(*file)
		}
		if err != nil {
			return fmt.Errorf("读取输入失败: %w", err), 1
		}
		text = string(data)
	} else {
		if fs.NArg() == 0 {
			if s := ctx.Summary(); s != "" {
				fmt.Println(s)
				return nil, 0
			}
			fmt.Println("（总评为空）")
			return nil, 0
		}
		text = strings.Join(fs.Args(), " ")
	}
	if err := ctx.Service.SetSummary(ctx.TargetID(), strings.TrimRight(text, "\n")); err != nil {
		return err, 1
	}
	if g.json {
		_ = printJSON(map[string]any{"summary": strings.TrimRight(text, "\n")})
	} else {
		fmt.Println("总评已保存")
	}
	return nil, 0
}

func cmdValidate(args []string) (error, int) {
	var g globalFlags
	args = parseGlobal(&g, args)
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("参数错误: %w", err), 1
	}
	ctx, err := loadCtx(g, g.target)
	if err != nil {
		return err, 1
	}
	cov, files := ctx.Overview()
	entries := ctx.Entries()
	if g.json {
		if err := printJSON(map[string]any{
			"target_id": ctx.TargetID(),
			"complete":  cov.Complete,
			"coverage":  cov,
			"files":     files,
			"entries":   entries,
			"summary":   ctx.Summary(),
		}); err != nil {
			return err, 1
		}
	} else {
		fmt.Printf("target %s\n", ctx.TargetID())
		fmt.Printf("解释条目: %d\n", len(entries))
		fmt.Printf("覆盖率: %d/%d 变更行", cov.CoveredLines, cov.TotalLines)
		if cov.FileUnitsTotal > 0 {
			fmt.Printf(", %d/%d 文件级", cov.FileUnitsCovered, cov.FileUnitsTotal)
		}
		if cov.Complete {
			fmt.Println(" — 完整 ✓")
		} else {
			fmt.Println(" — 不完整")
			for _, u := range cov.Uncovered {
				fmt.Printf("  未解释: %s:%s 行 %s（%s）\n", u.File, u.Side, joinInts(u.Lines), strings.Join(u.Hunks, ","))
			}
			for _, f := range cov.UncoveredFiles {
				fmt.Printf("  未解释: %s（文件级）\n", f)
			}
		}
	}
	if !cov.Complete {
		return exitf(2, "覆盖率不完整"), 2
	}
	return nil, 0
}

func cmdDone(args []string) (error, int) {
	var g globalFlags
	args = parseGlobal(&g, args)
	fs := flag.NewFlagSet("done", flag.ContinueOnError)
	fs.Usage = func() {}
	force := fs.Bool("force", false, "即使覆盖不完整也标记完成")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("参数错误: %w", err), 1
	}
	ctx, err := loadCtx(g, g.target)
	if err != nil {
		return err, 1
	}
	cov := ctx.Service.Coverage(ctx.TargetID())
	if !cov.Complete && !*force {
		return exitf(2, "覆盖率不完整（%d/%d 行, %d/%d 文件级），使用 --force 强制标记完成", cov.CoveredLines, cov.TotalLines, cov.FileUnitsCovered, cov.FileUnitsTotal), 2
	}
	rec := session.Record{
		TargetID: ctx.TargetID(),
		Branch:   ctx.Meta.Branch,
		Base:     ctx.Meta.Base,
		Head:     ctx.Meta.Head,
		DoneAt:   time.Now().UTC().Format(time.RFC3339),
		Forced:   !cov.Complete,
	}
	if err := session.MarkDone(ctx.YacrDir, rec); err != nil {
		return err, 1
	}
	if g.json {
		_ = printJSON(map[string]any{"done": true, "record": rec})
	} else {
		fmt.Printf("已标记完成: %s（head %s）\n", ctx.TargetID(), gitcmd.Short(ctx.Meta.Head))
		fmt.Println("下次 `yacr task` 将默认以该 head 为 base 做增量 review")
	}
	return nil, 0
}

func repeated(fs *flag.FlagSet, name, usage string) *[]string {
	var v []string
	fs.Func(name, usage, func(s string) error {
		v = append(v, s)
		return nil
	})
	return &v
}

func shortCommits(shas []string) string {
	parts := make([]string, 0, len(shas))
	for _, s := range shas {
		parts = append(parts, gitcmd.Short(s))
	}
	return strings.Join(parts, ",")
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " ..."
	}
	if len([]rune(s)) > 80 {
		r := []rune(s)
		return string(r[:80]) + "..."
	}
	return s
}
