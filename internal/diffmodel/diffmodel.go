package diffmodel

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Side string

const (
	SideOld Side = "old"
	SideNew Side = "new"
)

func (s Side) Valid() bool { return s == SideOld || s == SideNew }

func (s Side) Opposite() Side {
	if s == SideOld {
		return SideNew
	}
	return SideOld
}

type FileStatus string

const (
	StatusAdded    FileStatus = "added"
	StatusDeleted  FileStatus = "deleted"
	StatusModified FileStatus = "modified"
	StatusRenamed  FileStatus = "renamed"
	StatusCopied   FileStatus = "copied"
)

type LineType byte

const (
	TypeContext LineType = ' '
	TypeDel     LineType = '-'
	TypeAdd     LineType = '+'
)

type Line struct {
	Type  LineType
	OldNo int
	NewNo int
	Text  string
}

type Hunk struct {
	ID       string
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Section  string
	Lines    []Line
}

func (h *Hunk) Span(side Side) (int, int, bool) {
	if side == SideOld {
		if h.OldCount == 0 {
			return 0, 0, false
		}
		return h.OldStart, h.OldStart + h.OldCount - 1, true
	}
	if h.NewCount == 0 {
		return 0, 0, false
	}
	return h.NewStart, h.NewStart + h.NewCount - 1, true
}

func (h *Hunk) ChangedLines(side Side) []int {
	var out []int
	for _, ln := range h.Lines {
		if side == SideOld && ln.Type == TypeDel {
			out = append(out, ln.OldNo)
		}
		if side == SideNew && ln.Type == TypeAdd {
			out = append(out, ln.NewNo)
		}
	}
	return out
}

type File struct {
	ID      string
	OldPath string
	NewPath string
	Status  FileStatus
	Binary  bool
	OldMode string
	NewMode string
	Hunks   []*Hunk

	changedCache map[Side]map[int]bool
}

func (f *File) IsChangedLine(side Side, line int) bool {
	if f.changedCache == nil {
		f.changedCache = make(map[Side]map[int]bool)
	}
	set := f.changedCache[side]
	if set == nil {
		set = make(map[int]bool)
		for _, h := range f.Hunks {
			for _, ln := range h.Lines {
				if side == SideOld && ln.Type == TypeDel {
					set[ln.OldNo] = true
				}
				if side == SideNew && ln.Type == TypeAdd {
					set[ln.NewNo] = true
				}
			}
		}
		f.changedCache[side] = set
	}
	return set[line]
}

func (f *File) Path() string {
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

func (f *File) PathFor(side Side) string {
	if side == SideOld {
		if f.OldPath != "" {
			return f.OldPath
		}
		return f.NewPath
	}
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

func (f *File) HasHunks() bool { return len(f.Hunks) > 0 }

func (f *File) IsLineInSpan(side Side, line int) bool {
	for _, h := range f.Hunks {
		s, e, ok := h.Span(side)
		if ok && line >= s && line <= e {
			return true
		}
	}
	return false
}

func (f *File) ChangedLines(side Side) []int {
	var out []int
	for _, h := range f.Hunks {
		out = append(out, h.ChangedLines(side)...)
	}
	sort.Ints(out)
	return out
}

type Model struct {
	Files []*File
}

func (m *Model) FileByID(id string) *File {
	for _, f := range m.Files {
		if f.ID == id {
			return f
		}
	}
	return nil
}

func (m *Model) FileByPath(path string, side Side) *File {
	for _, f := range m.Files {
		if f.PathFor(side) == path {
			return f
		}
	}
	return nil
}

type LineRef struct {
	FileID string
	Path   string
	Side   Side
	Line   int
}

func (m *Model) AllChangedLines() map[LineRef]string {
	res := make(map[LineRef]string)
	for _, f := range m.Files {
		for _, h := range f.Hunks {
			for _, ln := range h.Lines {
				if ln.Type == TypeDel {
					res[LineRef{f.ID, f.PathFor(SideOld), SideOld, ln.OldNo}] = h.ID
				} else if ln.Type == TypeAdd {
					res[LineRef{f.ID, f.PathFor(SideNew), SideNew, ln.NewNo}] = h.ID
				}
			}
		}
	}
	return res
}

func (m *Model) FileLevelUnits() []*File {
	var out []*File
	for _, f := range m.Files {
		if !f.HasHunks() {
			out = append(out, f)
		}
	}
	return out
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@ ?(.*)$`)

type parser struct {
	files   []*File
	cur     *File
	hunk    *Hunk
	remOld  int
	remNew  int
	oldNo   int
	newNo   int
	fileSeq int
	sawDiff bool
}

func Parse(text string) (*Model, error) {
	p := &parser{}
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, raw := range lines {
		ln := strings.TrimRight(raw, "\r")
		if err := p.parseLine(ln); err != nil {
			return nil, fmt.Errorf("解析 diff 第 %d 行 %q 失败: %w", i+1, truncate(ln, 60), err)
		}
	}
	p.flush()
	if !p.sawDiff {
		return nil, fmt.Errorf("不是有效的 git diff 输出（缺少 diff --git 头）")
	}
	return &Model{Files: p.files}, nil
}

func (p *parser) parseLine(ln string) error {
	if p.hunk != nil {
		return p.parseBodyLine(ln)
	}
	if strings.HasPrefix(ln, `\ No newline`) {
		return nil
	}
	switch {
	case ln == "":
		return nil
	case strings.HasPrefix(ln, "diff --git "):
		p.flush()
		p.sawDiff = true
		p.fileSeq++
		id := fmt.Sprintf("F%02d", p.fileSeq)
		p.cur = &File{ID: id}
		p.parseHeaderPaths(strings.TrimPrefix(ln, "diff --git "))
		return nil
	case p.cur == nil:
		return fmt.Errorf("unexpected line before diff header")
	case strings.HasPrefix(ln, "@@ "):
		m := hunkRe.FindStringSubmatch(ln)
		if m == nil {
			return fmt.Errorf("hunk 头格式错误")
		}
		h := &Hunk{Section: m[5]}
		h.OldStart = atoi(m[1])
		h.OldCount = defaultOne(m[2])
		h.NewStart = atoi(m[3])
		h.NewCount = defaultOne(m[4])
		if h.OldStart == 0 {
			h.OldCount = 0
		}
		if h.NewStart == 0 {
			h.NewCount = 0
		}
		p.cur.Hunks = append(p.cur.Hunks, h)
		p.hunk = h
		p.remOld, p.remNew = h.OldCount, h.NewCount
		p.oldNo, p.newNo = h.OldStart, h.NewStart
		return nil
	case strings.HasPrefix(ln, "old mode "):
		p.cur.OldMode = strings.TrimPrefix(ln, "old mode ")
	case strings.HasPrefix(ln, "new mode "):
		p.cur.NewMode = strings.TrimPrefix(ln, "new mode ")
	case strings.HasPrefix(ln, "new file mode"):
		p.cur.Status = StatusAdded
		p.cur.NewMode = strings.TrimSpace(strings.TrimPrefix(ln, "new file mode"))
	case strings.HasPrefix(ln, "deleted file mode"):
		p.cur.Status = StatusDeleted
		p.cur.OldMode = strings.TrimSpace(strings.TrimPrefix(ln, "deleted file mode"))
	case strings.HasPrefix(ln, "rename from "):
		p.cur.OldPath = strings.TrimPrefix(ln, "rename from ")
		p.cur.Status = StatusRenamed
	case strings.HasPrefix(ln, "rename to "):
		p.cur.NewPath = strings.TrimPrefix(ln, "rename to ")
		p.cur.Status = StatusRenamed
	case strings.HasPrefix(ln, "copy from "):
		p.cur.OldPath = strings.TrimPrefix(ln, "copy from ")
		p.cur.Status = StatusCopied
	case strings.HasPrefix(ln, "copy to "):
		p.cur.NewPath = strings.TrimPrefix(ln, "copy to ")
		p.cur.Status = StatusCopied
	case strings.HasPrefix(ln, "similarity index") || strings.HasPrefix(ln, "dissimilarity index"):
	case strings.HasPrefix(ln, "index "):
	case strings.HasPrefix(ln, "Binary files "):
		p.cur.Binary = true
	case ln == "GIT binary patch":
		p.cur.Binary = true
	case strings.HasPrefix(ln, "--- "):
		path := strings.TrimPrefix(ln, "--- ")
		if path == "/dev/null" {
			p.cur.OldPath = ""
		} else {
			p.cur.OldPath = trimAB(stripTab(path))
		}
	case strings.HasPrefix(ln, "+++ "):
		path := strings.TrimPrefix(ln, "+++ ")
		if path == "/dev/null" {
			p.cur.NewPath = ""
		} else {
			p.cur.NewPath = trimAB(stripTab(path))
		}
	default:
		return fmt.Errorf("无法识别的行")
	}
	return nil
}

func (p *parser) parseBodyLine(ln string) error {
	if strings.HasPrefix(ln, `\ No newline`) {
		return nil
	}
	if p.remOld <= 0 && p.remNew <= 0 {
		p.hunk = nil
		return p.parseLine(ln)
	}
	if ln == "" {
		if p.remOld > 0 || p.remNew > 0 {
			return fmt.Errorf("hunk 内容不完整")
		}
		p.hunk = nil
		return nil
	}
	switch ln[0] {
	case ' ':
		if p.remOld <= 0 || p.remNew <= 0 {
			return fmt.Errorf("context 行超出 hunk 范围")
		}
		p.hunk.Lines = append(p.hunk.Lines, Line{TypeContext, p.oldNo, p.newNo, ln[1:]})
		p.remOld--
		p.remNew--
		p.oldNo++
		p.newNo++
	case '-':
		if p.remOld <= 0 {
			return fmt.Errorf("删除行超出 hunk 范围")
		}
		p.hunk.Lines = append(p.hunk.Lines, Line{TypeDel, p.oldNo, 0, ln[1:]})
		p.remOld--
		p.oldNo++
	case '+':
		if p.remNew <= 0 {
			return fmt.Errorf("新增行超出 hunk 范围")
		}
		p.hunk.Lines = append(p.hunk.Lines, Line{TypeAdd, 0, p.newNo, ln[1:]})
		p.remNew--
		p.newNo++
	default:
		return fmt.Errorf("hunk 正文中出现非法前缀 %q", string(ln[0]))
	}
	if p.remOld <= 0 && p.remNew <= 0 {
		p.hunk = nil
	}
	return nil
}

func (p *parser) parseHeaderPaths(rest string) {
	if strings.HasPrefix(rest, `"`) {
		a, b, err := unquotePair(rest)
		if err == nil {
			p.cur.OldPath, p.cur.NewPath = a, b
		}
		return
	}
	if !strings.HasPrefix(rest, "a/") {
		return
	}
	i := strings.Index(rest, " b/")
	if i < 0 {
		return
	}
	old, newp := rest[2:i], rest[i+3:]
	if old == newp {
		p.cur.OldPath, p.cur.NewPath = old, newp
		return
	}
	if p.cur.OldPath == "" {
		p.cur.OldPath = old
	}
	if p.cur.NewPath == "" {
		p.cur.NewPath = newp
	}
}

func unquotePair(s string) (string, string, error) {
	parts := strings.Split(s, `" "`)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("bad quoted header")
	}
	a, err1 := strconv.Unquote(parts[0])
	b, err2 := strconv.Unquote(parts[1])
	if err1 != nil || err2 != nil {
		return "", "", fmt.Errorf("bad quoted header")
	}
	return trimAB(a), trimAB(b), nil
}

func trimAB(p string) string {
	if len(p) >= 2 && (p[0] == 'a' || p[0] == 'b') && p[1] == '/' {
		return p[2:]
	}
	return p
}

func (p *parser) flush() {
	if p.cur == nil {
		return
	}
	f := p.cur
	p.cur = nil
	p.hunk = nil
	p.remOld, p.remNew = 0, 0
	if f.OldPath == "" && f.NewPath == "" {
		return
	}
	if f.Status == "" {
		f.Status = StatusModified
	}
	for i, h := range f.Hunks {
		h.ID = fmt.Sprintf("%s.H%02d", f.ID, i+1)
	}
	p.files = append(p.files, f)
}

func stripTab(s string) string {
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		return s[:i]
	}
	return s
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func defaultOne(s string) int {
	if s == "" {
		return 1
	}
	return atoi(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
