package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"yacr/internal/diffmodel"
	"yacr/internal/gitcmd"
	"yacr/internal/report"
	"yacr/internal/taskgen"
)

type Ctx struct {
	Repo    *gitcmd.Git
	RepoDir string
	YacrDir string
	TaskDir string
	Meta    *taskgen.Meta
	Model   *diffmodel.Model
	Service *report.Service
}

func FindRepoDir(start string) (string, error) {
	g := gitcmd.New(start)
	top, err := g.TopLevel()
	if err != nil {
		return "", fmt.Errorf("当前目录不是 git 仓库: %w", err)
	}
	return top, nil
}

func NewGit(repoDir string) *gitcmd.Git { return gitcmd.New(repoDir) }

func Load(repoDir, targetID string) (*Ctx, error) {
	yacrDir := filepath.Join(repoDir, ".yacr")
	if targetID == "" {
		b, err := os.ReadFile(filepath.Join(yacrDir, taskgen.CurrentFileName))
		if err != nil {
			return nil, fmt.Errorf("尚未生成 review 任务，请先运行 `yacr task`")
		}
		targetID = strings.TrimSpace(string(b))
	}
	taskDir := filepath.Join(yacrDir, "tasks", targetID)
	metaBytes, err := os.ReadFile(filepath.Join(taskDir, "meta.json"))
	if err != nil {
		return nil, fmt.Errorf("任务 %s 不存在（目录 %s）: %w", targetID, taskDir, err)
	}
	var meta taskgen.Meta
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return nil, fmt.Errorf("meta.json 损坏: %w", err)
	}
	patch, err := os.ReadFile(filepath.Join(taskDir, "diff.patch"))
	if err != nil {
		return nil, fmt.Errorf("diff.patch 缺失: %w", err)
	}
	model, err := diffmodel.Parse(string(patch))
	if err != nil {
		return nil, fmt.Errorf("diff.patch 损坏: %w", err)
	}
	commits := make([]string, 0, len(meta.Commits))
	for _, c := range meta.Commits {
		commits = append(commits, c.SHA)
	}
	return &Ctx{
		Repo:    gitcmd.New(repoDir),
		RepoDir: repoDir,
		YacrDir: yacrDir,
		TaskDir: taskDir,
		Meta:    &meta,
		Model:   model,
		Service: report.OpenService(yacrDir, model, commits),
	}, nil
}

func (c *Ctx) TargetID() string { return c.Meta.TargetID }

type FileOverview struct {
	ID              string `json:"id"`
	Path            string `json:"path"`
	OldPath         string `json:"old_path,omitempty"`
	Status          string `json:"status"`
	Binary          bool   `json:"binary,omitempty"`
	Hunks           int    `json:"hunks"`
	ChangedNew      int    `json:"changed_new"`
	ChangedOld      int    `json:"changed_old"`
	UncoveredNew    []int  `json:"uncovered_new,omitempty"`
	UncoveredOld    []int  `json:"uncovered_old,omitempty"`
	FileUnit        bool   `json:"file_unit"`
	FileUnitCovered bool   `json:"file_unit_covered,omitempty"`
}

func (c *Ctx) Overview() (*report.Coverage, []FileOverview) {
	cov := c.Service.Coverage(c.TargetID())
	uncoveredLines := map[string]map[diffmodel.Side]map[int]bool{}
	for _, g := range cov.Uncovered {
		side := diffmodel.Side(g.Side)
		m := uncoveredLines[g.File]
		if m == nil {
			m = map[diffmodel.Side]map[int]bool{}
			uncoveredLines[g.File] = m
		}
		s := m[side]
		if s == nil {
			s = map[int]bool{}
			m[side] = s
		}
		for _, ln := range g.Lines {
			s[ln] = true
		}
	}
	uncoveredFiles := map[string]bool{}
	for _, f := range cov.UncoveredFiles {
		uncoveredFiles[f] = true
	}

	var out []FileOverview
	for _, f := range c.Model.Files {
		fo := FileOverview{
			ID:     f.ID,
			Path:   f.Path(),
			Status: string(f.Status),
			Binary: f.Binary,
			Hunks:  len(f.Hunks),
		}
		if f.OldPath != "" && f.OldPath != f.NewPath {
			fo.OldPath = f.OldPath
		}
		if f.HasHunks() {
			fo.ChangedNew = len(f.ChangedLines(diffmodel.SideNew))
			fo.ChangedOld = len(f.ChangedLines(diffmodel.SideOld))
			if m := uncoveredLines[f.PathFor(diffmodel.SideNew)]; m != nil {
				for ln := range m[diffmodel.SideNew] {
					fo.UncoveredNew = append(fo.UncoveredNew, ln)
				}
			}
			if m := uncoveredLines[f.PathFor(diffmodel.SideOld)]; m != nil {
				for ln := range m[diffmodel.SideOld] {
					fo.UncoveredOld = append(fo.UncoveredOld, ln)
				}
			}
			sort.Ints(fo.UncoveredNew)
			sort.Ints(fo.UncoveredOld)
		} else {
			fo.FileUnit = true
			fo.FileUnitCovered = !uncoveredFiles[f.Path()]
		}
		out = append(out, fo)
	}
	return cov, out
}

func (c *Ctx) Entries() []*report.Entry {
	return c.Service.Load(c.TargetID()).Entries
}

func (c *Ctx) Summary() string {
	return c.Service.Load(c.TargetID()).Summary
}

func (c *Ctx) EntriesCovering(file string, side diffmodel.Side, line int) []*report.Entry {
	var out []*report.Entry
	for _, e := range c.Entries() {
		for _, loc := range e.Locations {
			if loc.File != file {
				continue
			}
			if loc.Side == "" {
				continue
			}
			if diffmodel.Side(loc.Side) == side && line >= loc.Start && line <= loc.End {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

type LocationQuery struct {
	File string
	Line int
	End  int
	Side string
}

func (c *Ctx) FindHunk(file string, side diffmodel.Side, line int) (*diffmodel.File, *diffmodel.Hunk) {
	f := c.Model.FileByPath(file, side)
	if f == nil {
		return nil, nil
	}
	for _, h := range f.Hunks {
		if s, e, ok := h.Span(side); ok && line >= s && line <= e {
			return f, h
		}
	}
	return f, nil
}

func (c *Ctx) Query(q LocationQuery) (map[string]any, error) {
	side := diffmodel.Side(q.Side)
	if q.Side == "" {
		if _, h := c.FindHunk(q.File, diffmodel.SideNew, q.Line); h != nil {
			side = diffmodel.SideNew
		} else if _, h := c.FindHunk(q.File, diffmodel.SideOld, q.Line); h != nil {
			side = diffmodel.SideOld
		}
		if side == "" {
			f := c.Model.FileByPath(q.File, diffmodel.SideNew)
			if f == nil {
				f = c.Model.FileByPath(q.File, diffmodel.SideOld)
			}
			if f == nil {
				return nil, fmt.Errorf("文件 %s 不在本次 diff 中", q.File)
			}
			return map[string]any{
				"file":  f.Path(),
				"found": false,
				"note":  "该行不在任何变更 hunk 内",
			}, nil
		}
	}
	f, h := c.FindHunk(q.File, side, q.Line)
	if f == nil {
		return nil, fmt.Errorf("文件 %s 的 %s 侧不存在", q.File, side)
	}
	if h == nil {
		return map[string]any{
			"file": f.Path(), "side": string(side), "found": false,
			"note": "该行不在任何变更 hunk 内",
		}, nil
	}
	changed := f.IsChangedLine(side, q.Line)
	entries := c.EntriesCovering(f.PathFor(side), side, q.Line)
	entryViews := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		entryViews = append(entryViews, map[string]any{
			"id": e.ID, "slug": e.Slug, "title": e.Title, "explanation": e.Explanation,
		})
	}
	return map[string]any{
		"file":    f.PathFor(side),
		"side":    string(side),
		"line":    q.Line,
		"hunk":    h.ID,
		"changed": changed,
		"covered": len(entries) > 0,
		"found":   true,
		"entries": entryViews,
	}, nil
}

type HunkLineView struct {
	Type     string   `json:"type"`
	OldNo    int      `json:"old_no,omitempty"`
	NewNo    int      `json:"new_no,omitempty"`
	Text     string   `json:"text"`
	Changed  bool     `json:"changed"`
	Covered  bool     `json:"covered"`
	EntryIDs []string `json:"entry_ids,omitempty"`
}

type HunkView struct {
	ID       string         `json:"id"`
	File     string         `json:"file"`
	Status   string         `json:"status"`
	OldStart int            `json:"old_start"`
	OldCount int            `json:"old_count"`
	NewStart int            `json:"new_start"`
	NewCount int            `json:"new_count"`
	Section  string         `json:"section,omitempty"`
	Lines    []HunkLineView `json:"lines"`
}

func (c *Ctx) HunkView(hunkID string) (*HunkView, error) {
	for _, f := range c.Model.Files {
		for _, h := range f.Hunks {
			if h.ID != hunkID {
				continue
			}
			hv := &HunkView{
				ID: h.ID, File: f.Path(), Status: string(f.Status),
				OldStart: h.OldStart, OldCount: h.OldCount,
				NewStart: h.NewStart, NewCount: h.NewCount,
				Section: h.Section,
			}
			for _, ln := range h.Lines {
				v := HunkLineView{Text: ln.Text}
				switch ln.Type {
				case diffmodel.TypeAdd:
					v.Type, v.NewNo, v.Changed = "add", ln.NewNo, true
				case diffmodel.TypeDel:
					v.Type, v.OldNo, v.Changed = "del", ln.OldNo, true
				default:
					v.Type, v.OldNo, v.NewNo = "context", ln.OldNo, ln.NewNo
				}
				if v.Changed {
					side := diffmodel.SideNew
					if v.Type == "del" {
						side = diffmodel.SideOld
					}
					lineNo := v.NewNo
					if side == diffmodel.SideOld {
						lineNo = v.OldNo
					}
					entries := c.EntriesCovering(f.PathFor(side), side, lineNo)
					v.Covered = len(entries) > 0
					for _, e := range entries {
						v.EntryIDs = append(v.EntryIDs, e.ID)
					}
				}
				hv.Lines = append(hv.Lines, v)
			}
			return hv, nil
		}
	}
	return nil, fmt.Errorf("hunk %s 不存在", hunkID)
}
