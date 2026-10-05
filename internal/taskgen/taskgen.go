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
	LastReviewed func(branch string) (string, bool)
	Now          func() time.Time
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
		infos = append(infos, CommitInfo{SHA: c.SHA, Subject: c.Subject, Body: c.Body, Files: files})
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
	out, uerr := g.Run("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if uerr == nil {
		up := strings.TrimSpace(out)
		if up != "" {
			sha, err := g.MergeBase(up, "HEAD")
			if err == nil {
				return sha, "upstream " + up, false, nil
			}
		}
	}
	for _, cand := range []string{"develop", "dev", "main", "master"} {
		if g.HasRef(cand) {
			sha, err := g.MergeBase(cand, "HEAD")
			if err == nil {
				return sha, cand, false, nil
			}
		}
	}
	return "", "", false, fmt.Errorf("无法确定 review 范围：未指定 --base、无已完成的 review 记录、无 upstream，且 develop/main/master 均不存在")
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
