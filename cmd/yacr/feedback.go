package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"yacr/internal/app"
	"yacr/internal/gitcmd"
)

func cmdFeedback(args []string) (error, int) {
	fs := flag.NewFlagSet("feedback", flag.ContinueOnError)
	fs.Usage = func() {}
	out := fs.String("o", "", "输出文件（默认 stdout）")
	var g globalFlags
	rest := parseGlobal(&g, args)
	if err := fs.Parse(rest); err != nil {
		return fmt.Errorf("参数错误: %w", err), 1
	}
	ctx, err := loadCtx(g, g.target)
	if err != nil {
		return err, 1
	}
	md := renderFeedback(ctx)
	if *out != "" {
		if err := os.WriteFile(*out, []byte(md), 0o644); err != nil {
			return fmt.Errorf("写入失败: %w", err), 1
		}
		if !g.json {
			fmt.Printf("已写入 %s\n", *out)
		}
		return nil, 0
	}
	fmt.Print(md)
	return nil, 0
}

func renderFeedback(ctx *app.Ctx) string {
	cov, files := ctx.Overview()
	entries := ctx.Entries()
	var b strings.Builder

	b.WriteString(fmt.Sprintf("# Review 摘要 %s\n\n", ctx.TargetID()))
	b.WriteString(fmt.Sprintf("- 分支: `%s`（base `%s` → head `%s`）\n", ctx.Meta.Branch, gitcmd.Short(ctx.Meta.Base), gitcmd.Short(ctx.Meta.Head)))
	b.WriteString(fmt.Sprintf("- 提交: %d 个, 文件: %d 个, 变更行: %d, 文件级条目: %d\n", len(ctx.Meta.Commits), len(files), cov.TotalLines, cov.FileUnitsTotal))
	b.WriteString(fmt.Sprintf("- 覆盖率: %d/%d 变更行", cov.CoveredLines, cov.TotalLines))
	if cov.FileUnitsTotal > 0 {
		b.WriteString(fmt.Sprintf(", %d/%d 文件级", cov.FileUnitsCovered, cov.FileUnitsTotal))
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("- 生成时间: %s\n", time.Now().Format("2006-01-02 15:04:05")))

	b.WriteString("\n## 提交列表\n\n")
	for _, c := range ctx.Meta.Commits {
		b.WriteString(fmt.Sprintf("- `%s` %s\n", gitcmd.Short(c.SHA), c.Subject))
	}

	if s := ctx.Summary(); s != "" {
		b.WriteString("\n## 总评\n\n")
		b.WriteString(s + "\n")
	}

	if len(entries) > 0 {
		b.WriteString("\n## 解释条目\n")
		for _, e := range entries {
			b.WriteString(fmt.Sprintf("\n### [%s] %s\n\n", e.ID, e.Title))
			for _, loc := range e.Locations {
				b.WriteString(fmt.Sprintf("- 定位: `%s`\n", loc.String()))
			}
			if len(e.Commits) > 0 {
				parts := make([]string, 0, len(e.Commits))
				for _, c := range e.Commits {
					parts = append(parts, "`"+gitcmd.Short(c)+"`")
				}
				b.WriteString(fmt.Sprintf("- commits: %s\n", strings.Join(parts, " ")))
			}
			if len(e.Tags) > 0 {
				b.WriteString(fmt.Sprintf("- tags: %s\n", strings.Join(e.Tags, ", ")))
			}
			b.WriteString("\n" + e.Explanation + "\n")
		}
	}

	if !cov.Complete {
		b.WriteString("\n## 未解释的变更（AI 尚未覆盖）\n\n")
		for _, u := range cov.Uncovered {
			b.WriteString(fmt.Sprintf("- `%s:%s` 行 %s（hunk %s）\n", u.File, u.Side, joinInts(u.Lines), strings.Join(u.Hunks, ", ")))
		}
		for _, f := range cov.UncoveredFiles {
			b.WriteString(fmt.Sprintf("- `%s`（文件级条目未解释）\n", f))
		}
	}
	return b.String()
}
