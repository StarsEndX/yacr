package tui

import (
	"fmt"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"yacr/internal/app"
	"yacr/internal/diffmodel"
	"yacr/internal/report"
)

type screen int

const (
	screenOverview screen = iota
	screenEntry
	screenDiff
)

type model struct {
	ctx     *app.Ctx
	cov     *report.Coverage
	files   []app.FileOverview
	entries []*report.Entry
	rows    []overviewRow

	screen     screen
	ovIdx      int
	entry      *report.Entry
	entryLines []string
	entryLocs  []string
	locIdx     int
	diffFile   int
	diffLines  []string
	diffTitle  string
	scroll     int
	offsetIdx  int

	width  int
	height int
	err    error
}

func Run(ctx *app.Ctx) error {
	cov, files := ctx.Overview()
	m := &model{ctx: ctx, cov: cov, files: files, entries: ctx.Entries(), width: 80, height: 24}
	m.rows = buildOverviewRows(cov, files, m.entries)
	p := bubbletea.NewProgram(m, bubbletea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m *model) Init() bubbletea.Cmd { return nil }

func (m *model) Update(msg bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	switch msg := msg.(type) {
	case bubbletea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case bubbletea.KeyMsg:
		return m.handleKey(msg.String())
	}
	return m, nil
}

func (m *model) handleKey(key string) (bubbletea.Model, bubbletea.Cmd) {
	if key == "ctrl+c" {
		return m, bubbletea.Quit
	}
	switch m.screen {
	case screenOverview:
		switch key {
		case "q", "esc":
			return m, bubbletea.Quit
		case "j", "down":
			if m.ovIdx < len(m.rows)-1 {
				m.ovIdx++
			}
		case "k", "up":
			if m.ovIdx > 0 {
				m.ovIdx--
			}
		case "g":
			m.ovIdx = 0
		case "G":
			m.ovIdx = len(m.rows) - 1
		case "d":
			m.openDiffFile(0)
		case "n":
			m.openFirstUncoveredDiff()
		case "enter":
			if m.ovIdx < len(m.rows) {
				row := m.rows[m.ovIdx]
				switch row.kind {
				case "entry":
					m.openEntry(row.ref.(*report.Entry))
				default:
					m.openDiffForRef(row.ref)
				}
			}
		case "?":
		}
	case screenEntry:
		switch key {
		case "q", "esc":
			m.screen = screenOverview
		case "j", "down":
			if m.locIdx < len(m.entryLocs)-1 {
				m.locIdx++
			}
		case "k", "up":
			if m.locIdx > 0 {
				m.locIdx--
			}
		case "enter", "d":
			if m.locIdx < len(m.entry.Locations) {
				m.openDiffForLocation(m.entry.Locations[m.locIdx])
			}
		}
	case screenDiff:
		switch key {
		case "q", "esc":
			m.screen = screenOverview
		case "j", "down":
			m.scroll++
		case "k", "up":
			if m.scroll > 0 {
				m.scroll--
			}
		case "d", "pgdown":
			m.scroll += m.height - 4
		case "u", "pgup":
			m.scroll -= m.height - 4
			if m.scroll < 0 {
				m.scroll = 0
			}
		case "g":
			m.scroll = 0
		case "G":
			m.scroll = len(m.diffLines)
		case "n":
			if m.diffFile < len(m.ctx.Model.Files)-1 {
				m.openDiffFile(m.diffFile + 1)
			}
		case "p":
			if m.diffFile > 0 {
				m.openDiffFile(m.diffFile - 1)
			}
		case "N":
			m.openFirstUncoveredDiff()
		}
	}
	m.clampScroll()
	return m, nil
}

func (m *model) clampScroll() {
	max := len(m.diffLines) - (m.height - 4)
	if max < 0 {
		max = 0
	}
	if m.scroll > max {
		m.scroll = max
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func (m *model) openEntry(e *report.Entry) {
	m.entry = e
	m.screen = screenEntry
	m.locIdx = 0
	m.entryLocs = make([]string, 0, len(e.Locations))
	for _, l := range e.Locations {
		m.entryLocs = append(m.entryLocs, l.String())
	}
}

func (m *model) openDiffFile(idx int) {
	if idx < 0 || idx >= len(m.ctx.Model.Files) {
		return
	}
	m.diffFile = idx
	f := m.ctx.Model.Files[idx]
	m.diffLines = renderDiffFile(m.ctx, f, m.width)
	m.diffTitle = fmt.Sprintf("diff %d/%d", idx+1, len(m.ctx.Model.Files))
	m.scroll = 0
	m.screen = screenDiff
}

func (m *model) openFirstUncoveredDiff() {
	if len(m.cov.Uncovered) == 0 {
		return
	}
	target := m.cov.Uncovered[0].File
	for i, f := range m.ctx.Model.Files {
		if f.PathFor(diffmodel.SideNew) == target || f.PathFor(diffmodel.SideOld) == target {
			m.openDiffFile(i)
			return
		}
	}
}

func (m *model) openDiffForRef(ref any) {
	path := ""
	switch v := ref.(type) {
	case report.UncoveredGroup:
		path = v.File
	case string:
		path = v
	}
	if path == "" {
		return
	}
	for i, f := range m.ctx.Model.Files {
		if f.PathFor(diffmodel.SideNew) == path || f.PathFor(diffmodel.SideOld) == path {
			m.openDiffFile(i)
			return
		}
	}
}

func (m *model) openDiffForLocation(loc report.Location) {
	for i, f := range m.ctx.Model.Files {
		if f.PathFor(diffmodel.Side(loc.Side)) == loc.File ||
			f.Path() == loc.File ||
			f.OldPath == loc.File {
			m.openDiffFile(i)
			return
		}
	}
	m.openDiffFile(0)
}

func (m *model) View() string {
	switch m.screen {
	case screenEntry:
		return m.viewEntry()
	case screenDiff:
		return m.viewDiff()
	default:
		return m.viewOverview()
	}
}

func (m *model) viewOverview() string {
	var b strings.Builder
	b.WriteString(renderOverviewHeader(m.ctx, m.cov))
	b.WriteString("\n\n")
	if len(m.rows) == 0 {
		b.WriteString(goodStyle.Render("  所有变更均已解释 ✓"))
		b.WriteString("\n\n")
	} else {
		top := m.ovIdx - (m.height - 12)
		if top < 0 {
			top = 0
		}
		bottom := top + (m.height - 10)
		if bottom > len(m.rows) {
			bottom = len(m.rows)
		}
		if top > 0 {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  …（上方还有 %d 行）", top)))
			b.WriteString("\n")
		}
		for i := top; i < bottom; i++ {
			row := m.rows[i]
			prefix := "  "
			if i == m.ovIdx {
				prefix = selStyle.Render(" > ")
			}
			style := entryStyle
			if row.kind == "uncovered" || row.kind == "uncovered_file" {
				style = badStyle
			}
			b.WriteString(prefix + style.Render(truncateRunes(row.text, m.width-6)))
			b.WriteString("\n")
		}
		if bottom < len(m.rows) {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  …（下方还有 %d 行）", len(m.rows)-bottom)))
			b.WriteString("\n")
		}
	}
	if s := m.ctx.Summary(); s != "" && m.screen == screenOverview {
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  总评: " + firstLine(s)))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  j/k 移动 · enter 打开 · d 全部 diff · n 跳到未解释 · q 退出"))
	b.WriteString("\n")
	return b.String()
}

func (m *model) viewEntry() string {
	e := m.entry
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf(" %s  %s ", e.ID, e.Title)))
	b.WriteString("\n")
	if e.Slug != "" {
		b.WriteString(dimStyle.Render("  slug: " + e.Slug))
		b.WriteString("\n")
	}
	if len(e.Commits) > 0 {
		parts := make([]string, 0, len(e.Commits))
		for _, c := range e.Commits {
			parts = append(parts, c[:7])
		}
		b.WriteString(dimStyle.Render("  commits: " + strings.Join(parts, ", ")))
		b.WriteString("\n")
	}
	if len(e.Tags) > 0 {
		b.WriteString(dimStyle.Render("  tags: " + strings.Join(e.Tags, ", ")))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString("  解释:\n")
	for _, ln := range wrapText(e.Explanation, m.width-6, "    ") {
		b.WriteString(ln + "\n")
	}
	b.WriteString("\n  定位（enter 跳转 diff）:\n")
	for i, loc := range m.entryLocs {
		prefix := "    "
		if i == m.locIdx {
			prefix = selStyle.Render("  > ")
		}
		b.WriteString(prefix + loc + "\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  j/k 选定位 · enter/d 打开 diff · esc 返回 · q 退出"))
	b.WriteString("\n")
	return b.String()
}

func (m *model) viewDiff() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" " + m.diffTitle + " "))
	b.WriteString(dimStyle.Render("  ● 已解释  ○ 未解释"))
	b.WriteString("\n\n")
	visible := m.height - 5
	for i := m.scroll; i < m.scroll+visible && i < len(m.diffLines); i++ {
		b.WriteString(m.diffLines[i])
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  j/k/d/u 滚动 · n/p 切换文件 · N 跳到未解释 · esc 返回 · q 退出"))
	b.WriteString("\n")
	return b.String()
}

func wrapText(s string, width int, indent string) []string {
	var out []string
	for _, para := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if para == "" {
			out = append(out, "")
			continue
		}
		cur := ""
		for _, word := range strings.Split(para, " ") {
			if cur == "" {
				cur = word
			} else if len([]rune(cur))+1+len([]rune(word)) <= width {
				cur += " " + word
			} else {
				out = append(out, indent+cur)
				cur = word
			}
		}
		if cur != "" {
			out = append(out, indent+cur)
		}
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
