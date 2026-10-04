package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"yacr/internal/app"
	"yacr/internal/diffmodel"
	"yacr/internal/gitcmd"
	"yacr/internal/report"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	goodStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	badStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	entryStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))
	addStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	delStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	selStyle   = lipgloss.NewStyle().Bold(true).Reverse(true)
	helpStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	badgStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
)

type overviewRow struct {
	kind string
	id   string
	text string
	ref  any
}

func buildOverviewRows(cov *report.Coverage, files []app.FileOverview, entries []*report.Entry) []overviewRow {
	var rows []overviewRow
	for _, g := range cov.Uncovered {
		rows = append(rows, overviewRow{
			kind: "uncovered",
			text: fmt.Sprintf("未解释  %s:%s  行 %s", g.File, g.Side, joinInts(g.Lines)),
			ref:  g,
		})
	}
	for _, f := range cov.UncoveredFiles {
		rows = append(rows, overviewRow{
			kind: "uncovered_file",
			text: fmt.Sprintf("未解释  %s（文件级条目）", f),
			ref:  f,
		})
	}
	for _, e := range entries {
		locs := make([]string, 0, len(e.Locations))
		for _, l := range e.Locations {
			locs = append(locs, l.String())
		}
		rows = append(rows, overviewRow{
			kind: "entry",
			id:   e.ID,
			text: fmt.Sprintf("%s  %s  [%s]", e.ID, e.Title, strings.Join(locs, ", ")),
			ref:  e,
		})
	}
	return rows
}

func renderOverviewHeader(ctx *app.Ctx, cov *report.Coverage) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf(" yacr — %s ", ctx.TargetID())))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("分支 %s  %s..%s  %d commits  只读视图",
		ctx.Meta.Branch, gitcmd.Short(ctx.Meta.Base), gitcmd.Short(ctx.Meta.Head), len(ctx.Meta.Commits))))
	b.WriteString("\n")
	pct := 0
	if cov.TotalLines+cov.FileUnitsTotal > 0 {
		pct = (cov.CoveredLines + cov.FileUnitsCovered) * 100 / (cov.TotalLines + cov.FileUnitsTotal)
	}
	status := badStyle.Render(fmt.Sprintf("覆盖不完整 %d/%d 行", cov.CoveredLines, cov.TotalLines))
	if cov.Complete {
		status = goodStyle.Render(fmt.Sprintf("覆盖完整 %d/%d 行", cov.CoveredLines, cov.TotalLines))
	}
	barWidth := 30
	filled := pct * barWidth / 100
	bar := goodStyle.Render(strings.Repeat("█", filled)) + dimStyle.Render(strings.Repeat("░", barWidth-filled))
	b.WriteString(fmt.Sprintf("%s %s %d%%", bar, status, pct))
	if cov.FileUnitsTotal > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  文件级 %d/%d", cov.FileUnitsCovered, cov.FileUnitsTotal)))
	}
	return b.String()
}

func renderDiffFile(ctx *app.Ctx, f *diffmodel.File, width int) []string {
	covLines := map[diffmodel.LineRef]bool{}
	entryIDs := map[diffmodel.LineRef]string{}
	for _, e := range ctx.Entries() {
		for _, loc := range e.Locations {
			side := diffmodel.Side(loc.Side)
			if loc.Side == "" {
				continue
			}
			for ln := loc.Start; ln <= loc.End; ln++ {
				ref := diffmodel.LineRef{Path: loc.File, Side: side, Line: ln}
				if _, ok := entryIDs[ref]; !ok {
					entryIDs[ref] = e.ID
				}
				covLines[ref] = true
			}
		}
	}

	var out []string
	header := fmt.Sprintf(" %s  %s  (%s)", f.ID, f.Path(), f.Status)
	if f.OldPath != "" && f.OldPath != f.NewPath {
		header = fmt.Sprintf(" %s  %s → %s  (%s)", f.ID, f.OldPath, f.NewPath, f.Status)
	}
	out = append(out, titleStyle.Render(header))
	if !f.HasHunks() {
		note := "（无行级变更：binary / 纯重命名 / 权限变更）"
		covered := false
		for _, e := range ctx.Entries() {
			for _, loc := range e.Locations {
				if loc.Side == "" && (loc.File == f.Path() || loc.File == f.OldPath) {
					covered = true
					note = fmt.Sprintf("%s  ✓ 已由 %s 解释", note, e.ID)
				}
			}
		}
		if !covered {
			out = append(out, badStyle.Render(note))
		} else {
			out = append(out, goodStyle.Render(note))
		}
		return out
	}
	for _, h := range f.Hunks {
		out = append(out, badgStyle.Render(fmt.Sprintf(" %s  @@ -%d,%d +%d,%d @@", h.ID, h.OldStart, h.OldCount, h.NewStart, h.NewCount)))
		for _, ln := range h.Lines {
			var side diffmodel.Side
			lineNo := 0
			switch ln.Type {
			case diffmodel.TypeAdd:
				side, lineNo = diffmodel.SideNew, ln.NewNo
			case diffmodel.TypeDel:
				side, lineNo = diffmodel.SideOld, ln.OldNo
			default:
				side = diffmodel.SideNew
				lineNo = ln.NewNo
			}
			ref := diffmodel.LineRef{Path: f.PathFor(side), Side: side, Line: lineNo}
			prefix := " "
			if ln.Type == diffmodel.TypeAdd {
				prefix = "+"
			} else if ln.Type == diffmodel.TypeDel {
				prefix = "-"
			}
			oldS, newS := "   ", "   "
			if ln.OldNo > 0 {
				oldS = fmt.Sprintf("%3d", ln.OldNo)
			}
			if ln.NewNo > 0 {
				newS = fmt.Sprintf("%3d", ln.NewNo)
			}
			body := truncateRunes(ln.Text, width-16)
			var line string
			switch {
			case ln.Type == diffmodel.TypeContext:
				line = dimStyle.Render(fmt.Sprintf("   %s %s %s", oldS, newS, body))
			case covLines[ref]:
				line = fmt.Sprintf("%s %s %s", goodStyle.Render("●"), styleLine(addStyle, prefix, oldS, newS, body), dimStyle.Render("← "+entryIDs[ref]))
			default:
				line = fmt.Sprintf("%s %s", badStyle.Render("○"), styleLine(addStyle, prefix, oldS, newS, body))
			}
			out = append(out, line)
		}
	}
	return out
}

func styleLine(base lipgloss.Style, prefix, oldS, newS, body string) string {
	if prefix == "-" {
		base = delStyle
	}
	return base.Render(fmt.Sprintf("%s %s %s %s", prefix, oldS, newS, body))
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if n <= 0 {
		return ""
	}
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func joinInts(a []int) string {
	parts := make([]string, 0, len(a))
	for _, n := range a {
		parts = append(parts, fmt.Sprintf("%d", n))
	}
	return strings.Join(parts, ",")
}
