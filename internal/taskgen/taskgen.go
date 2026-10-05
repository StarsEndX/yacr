package taskgen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"yacr/internal/diffmodel"
	"yacr/internal/gitcmd"
)

const CurrentFileName = "current"

type CommitInfo struct {
	SHA     string              `json:"sha"`
	Subject string              `json:"subject"`
	Body    string              `json:"body"`
	Merge   bool                `json:"merge,omitempty"`
	Files   []gitcmd.FileChange `json:"files"`
}

type Meta struct {
	TargetID    string       `json:"target_id"`
	Base        string       `json:"base"`
	Head        string       `json:"head"`
	Branch      string       `json:"branch"`
	BaseRef     string       `json:"base_ref"`
	Incremental bool         `json:"incremental"`
	CreatedAt   string       `json:"created_at"`
	Commits     []CommitInfo `json:"commits"`
}

type Stats struct {
	Files        int `json:"files"`
	Hunks        int `json:"hunks"`
	ChangedLines int `json:"changed_lines"`
	FileUnits    int `json:"file_units"`
	Commits      int `json:"commits"`
}

type Result struct {
	Meta  *Meta
	Dir   string
	Model *diffmodel.Model
	Stats Stats
}

type Options struct {
	ExplicitBase string
	Range        string
	ConfigBase   string
	LastReviewed func(branch string) (string, bool)
	Now          func() time.Time
}

type BaseCandidate struct {
	Ref       string
	MergeBase string
	Ahead     int
	Contained bool
}

func BaseCandidates(g *gitcmd.Git, limit int) []BaseCandidate {
	branch, err := g.CurrentBranch()
	if err != nil {
		return nil
	}
	refs, err := g.Branches()
	if err != nil {
		return nil
	}
	var locals, remotes []string
	for _, ref := range refs {
		if ref == branch {
			continue
		}
		if strings.Contains(ref, "/") {
			remotes = append(remotes, ref)
		} else {
			locals = append(locals, ref)
		}
	}
	ordered := append(append([]string{}, locals...), remotes...)
	considered := ordered
	if len(considered) > 40 {
		considered = considered[:40]
	}
	var out []BaseCandidate
	seenTip := map[string]bool{}
	for _, ref := range considered {
		tip, err := g.ResolveCommit(ref)
		if err != nil {
			continue
		}
		if seenTip[tip] || tip == "" {
			continue
		}
		seenTip[tip] = true
		mb, err := g.MergeBase(ref, "HEAD")
		if err != nil || mb == "" {
			continue
		}
		ahead, err := g.CountCommits(mb, "HEAD")
		if err != nil || ahead == 0 {
			continue
		}
		contained, _ := g.IsAncestor(tip, "HEAD")
		out = append(out, BaseCandidate{Ref: ref, MergeBase: mb, Ahead: ahead, Contained: contained})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Contained != out[j].Contained {
			return out[i].Contained
		}
		return out[i].Ahead < out[j].Ahead
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func Generate(g *gitcmd.Git, yacrDir string, opts Options) (*Result, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	st, err := g.Status()
	if err != nil {
		return nil, fmt.Errorf("读取工作区状态失败: %w", err)
	}
	if len(st.DirtyTracked) > 0 {
		return nil, fmt.Errorf("工作区存在未提交的变更（%d 个文件），综合 diff 语义要求工作区干净；请先 commit 或 stash:\n  %s", len(st.DirtyTracked), strings.Join(st.DirtyTracked, "\n  "))
	}

	base, baseRef, incremental, err := resolveBase(g, opts)
	if err != nil {
		return nil, err
	}
	head, err := g.ResolveCommit("HEAD")
	if err != nil {
		return nil, fmt.Errorf("解析 HEAD 失败: %w", err)
	}
	if base == head {
		return nil, fmt.Errorf("范围内没有领先提交（base 与 HEAD 相同），无可 review 内容")
	}
	branch, err := g.CurrentBranch()
	if err != nil {
		return nil, fmt.Errorf("读取分支名失败: %w", err)
	}

	commits, err := g.Commits(base, head)
	if err != nil {
		return nil, err
	}
	if len(commits) == 0 {
		return nil, fmt.Errorf("范围内没有领先提交，无可 review 内容")
	}
	commitSet := make(map[string]bool, len(commits))
	for _, c := range commits {
		commitSet[c.SHA] = true
	}

	diffText, err := g.DiffUnified(base, head)
	if err != nil {
		return nil, err
	}
	model, err := diffmodel.Parse(diffText)
	if err != nil {
		return nil, fmt.Errorf("解析综合 diff 失败: %w", err)
	}

	infos := make([]CommitInfo, 0, len(commits))
	for _, c := range commits {
		files, err := g.CommitFiles(c.SHA)
		if err != nil {
			return nil, err
		}
		infos = append(infos, CommitInfo{SHA: c.SHA, Subject: c.Subject, Body: c.Body, Merge: len(c.Parents) > 1, Files: files})
	}

	targetID := gitcmd.Short(base) + ".." + gitcmd.Short(head)
	meta := &Meta{
		TargetID:    targetID,
		Base:        base,
		Head:        head,
		Branch:      branch,
		BaseRef:     baseRef,
		Incremental: incremental,
		CreatedAt:   opts.Now().UTC().Format(time.RFC3339),
		Commits:     infos,
	}

	changes, err := buildChanges(g, model, head, commitSet, infos)
	if err != nil {
		return nil, err
	}

	taskDir := filepath.Join(yacrDir, "tasks", targetID)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建任务目录失败: %w", err)
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{
		"meta.json":     metaBytes,
		"diff.patch":    []byte(diffText),
		"changes.jsonl": []byte(changes),
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(taskDir, name), b, 0o644); err != nil {
			return nil, fmt.Errorf("写入 %s 失败: %w", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(yacrDir, CurrentFileName), []byte(targetID), 0o644); err != nil {
		return nil, fmt.Errorf("写入 current 指针失败: %w", err)
	}

	stats := Stats{Commits: len(commits)}
	for _, f := range model.Files {
		stats.Files++
		stats.Hunks += len(f.Hunks)
		if !f.HasHunks() {
			stats.FileUnits++
		}
	}
	stats.ChangedLines = len(model.AllChangedLines())

	return &Result{Meta: meta, Dir: taskDir, Model: model, Stats: stats}, nil
}

func resolveBase(g *gitcmd.Git, opts Options) (sha string, refDesc string, incremental bool, err error) {
	if opts.Range != "" {
		parts := strings.SplitN(opts.Range, "..", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", false, fmt.Errorf("范围格式应为 <base>..<head>")
		}
		base, err := g.ResolveCommit(parts[0])
		if err != nil {
			return "", "", false, err
		}
		hi, err := g.ResolveCommit(parts[1])
		if err != nil {
			return "", "", false, err
		}
		head, err := g.ResolveCommit("HEAD")
		if err != nil {
			return "", "", false, err
		}
		if hi != head {
			return "", "", false, fmt.Errorf("--range 的 head 目前必须为 HEAD（review 固定针对当前状态）；如需 review 其他 head，请先 checkout 到对应提交")
		}
		return base, "range " + opts.Range, false, nil
	}
	if opts.ExplicitBase != "" {
		sha, err := g.MergeBase(opts.ExplicitBase, "HEAD")
		if err != nil {
			return "", "", false, fmt.Errorf("解析 --base %s 失败: %w", opts.ExplicitBase, err)
		}
		return sha, opts.ExplicitBase, false, nil
	}
	branch, berr := g.CurrentBranch()
	if berr == nil && opts.LastReviewed != nil {
		if sha, ok := opts.LastReviewed(branch); ok && sha != "" {
			ok2, aerr := g.IsAncestor(sha, "HEAD")
			if aerr == nil && ok2 {
				return sha, "reviewed-head(" + branch + ")", true, nil
			}
		}
	}
	if opts.ConfigBase != "" {
		if _, err := g.ResolveCommit(opts.ConfigBase); err != nil {
			return "", "", false, fmt.Errorf("config 中的 base_ref %q 无法解析: %w", opts.ConfigBase, err)
		}
		sha, err := g.MergeBase(opts.ConfigBase, "HEAD")
		if err != nil {
			return "", "", false, fmt.Errorf("解析 config base %s 失败: %w", opts.ConfigBase, err)
		}
		return sha, opts.ConfigBase + " (config)", false, nil
	}
	return "", "", false, refuseNoBase(g)
}

func refuseNoBase(g *gitcmd.Git) error {
	var b strings.Builder
	b.WriteString("无法确定 review 范围的 base：yacr 不做启发式猜测。\n")
	b.WriteString("base 应为变更同步源（变更从哪里流出，如 origin/master）；注意不是合入目标分支——\n")
	b.WriteString("若 feature 已包含同步源的 hotfix，对合入目标取 diff 会把这些内容误算进 review 范围。\n")
	if cands := BaseCandidates(g, 8); len(cands) > 0 {
		minAhead := cands[0].Ahead
		for _, c := range cands {
			if c.Ahead < minAhead {
				minAhead = c.Ahead
			}
		}
		b.WriteString("\n候选分支:\n")
		for _, c := range cands {
			note := ""
			if c.Ahead == minAhead {
				note = "  ← 领先最少（通常即本次工作）"
			}
			b.WriteString(fmt.Sprintf("  %-24s base %s  领先 %d commits%s\n", c.Ref, gitcmd.Short(c.MergeBase), c.Ahead, note))
		}
	} else {
		b.WriteString("\n（未找到可用候选分支）\n")
	}
	b.WriteString("\n确认方式:\n")
	b.WriteString("  一次性: yacr task --base <ref>\n")
	b.WriteString("  永久:   yacr config base <ref>   （写入 .yacr/config，之后 task 直接使用）")
	return fmt.Errorf("%s", b.String())
}

type changeRecord struct {
	Kind    string   `json:"kind"`
	File    string   `json:"file"`
	Side    string   `json:"side,omitempty"`
	Hunk    string   `json:"hunk,omitempty"`
	Lines   []int    `json:"lines,omitempty"`
	Commits []string `json:"commits,omitempty"`
}

func buildChanges(g *gitcmd.Git, model *diffmodel.Model, head string, commitSet map[string]bool, infos []CommitInfo) ([]byte, error) {
	var recs []changeRecord
	for _, f := range model.Files {
		if f.HasHunks() {
			blame, err := blameFile(g, f, head)
			if err != nil {
				return nil, err
			}
			for _, h := range f.Hunks {
				newLines := h.ChangedLines(diffmodel.SideNew)
				newCommits := distinctCommits(blame, newLines, commitSet)
				recs = append(recs, changeRecord{
					Kind: "lines", File: f.PathFor(diffmodel.SideNew), Side: "new",
					Hunk: h.ID, Lines: newLines, Commits: newCommits,
				})
				oldLines := h.ChangedLines(diffmodel.SideOld)
				if len(oldLines) > 0 {
					recs = append(recs, changeRecord{
						Kind: "lines", File: f.PathFor(diffmodel.SideOld), Side: "old",
						Hunk: h.ID, Lines: oldLines,
					})
				}
			}
		} else {
			recs = append(recs, changeRecord{
				Kind: "file", File: f.Path(), Commits: commitsTouching(f, infos),
			})
		}
	}
	var sb strings.Builder
	for _, r := range recs {
		b, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	return []byte(sb.String()), nil
}

func blameFile(g *gitcmd.Git, f *diffmodel.File, head string) (map[int]gitcmd.BlameLine, error) {
	var ranges [][2]int
	for _, h := range f.Hunks {
		if s, e, ok := h.Span(diffmodel.SideNew); ok {
			ranges = append(ranges, [2]int{s, e})
		}
	}
	if len(ranges) == 0 {
		return nil, nil
	}
	return g.BlameLines(head, f.PathFor(diffmodel.SideNew), ranges)
}

func distinctCommits(blame map[int]gitcmd.BlameLine, lines []int, commitSet map[string]bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, ln := range lines {
		bl, ok := blame[ln]
		if !ok || bl.Boundary || !commitSet[bl.SHA] {
			continue
		}
		if !seen[bl.SHA] {
			seen[bl.SHA] = true
			out = append(out, bl.SHA)
		}
	}
	sort.Strings(out)
	return out
}

func commitsTouching(f *diffmodel.File, infos []CommitInfo) []string {
	var out []string
	for _, ci := range infos {
		for _, fc := range ci.Files {
			if fc.Path == f.Path() || fc.OldPath == f.OldPath || fc.OldPath == f.NewPath {
				out = append(out, ci.SHA)
				break
			}
		}
	}
	return out
}
