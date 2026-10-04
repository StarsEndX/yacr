package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"yacr/internal/diffmodel"
)

func withLock(path string, fn func() error) error {
	lockFile := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建报告目录失败: %w", err)
	}
	f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("打开锁文件失败: %w", err)
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("获取报告锁失败: %w", err)
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return fn()
}

type Location struct {
	File  string `json:"file"`
	Side  string `json:"side,omitempty"`
	Start int    `json:"start,omitempty"`
	End   int    `json:"end,omitempty"`
}

func (l Location) String() string {
	if l.Side == "" {
		return l.File
	}
	if l.Start == l.End {
		return fmt.Sprintf("%s:%d(%s)", l.File, l.Start, l.Side)
	}
	return fmt.Sprintf("%s:%d-%d(%s)", l.File, l.Start, l.End, l.Side)
}

type Entry struct {
	ID          string     `json:"id"`
	Slug        string     `json:"slug,omitempty"`
	Title       string     `json:"title"`
	Explanation string     `json:"explanation"`
	Locations   []Location `json:"locations"`
	Commits     []string   `json:"commits,omitempty"`
	Tags        []string   `json:"tags,omitempty"`
	CreatedAt   string     `json:"created_at"`
	UpdatedAt   string     `json:"updated_at"`
}

type Report struct {
	Version  int      `json:"version"`
	TargetID string   `json:"target_id"`
	Summary  string   `json:"summary,omitempty"`
	Entries  []*Entry `json:"entries"`
}

type ValError struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Details []string `json:"details,omitempty"`
}

func (e *ValError) Error() string {
	if len(e.Details) > 0 {
		return e.Message + "\n  " + strings.Join(e.Details, "\n  ")
	}
	return e.Message
}

func valErr(code, format string, args ...any) *ValError {
	return &ValError{Code: code, Message: fmt.Sprintf(format, args...)}
}

type UncoveredGroup struct {
	File  string   `json:"file"`
	Side  string   `json:"side"`
	Lines []int    `json:"lines"`
	Hunks []string `json:"hunks"`
}

type Coverage struct {
	TotalLines       int              `json:"total_lines"`
	CoveredLines     int              `json:"covered_lines"`
	Uncovered        []UncoveredGroup `json:"uncovered"`
	FileUnitsTotal   int              `json:"file_units_total"`
	FileUnitsCovered int              `json:"file_units_covered"`
	UncoveredFiles   []string         `json:"uncovered_files"`
	Complete         bool             `json:"complete"`
}

type EntryInput struct {
	ID          string     `json:"id"`
	Slug        string     `json:"slug"`
	Title       string     `json:"title"`
	Explanation string     `json:"explanation"`
	Locations   []Location `json:"locations"`
	Commits     []string   `json:"commits"`
	Tags        []string   `json:"tags"`
}

type UpsertResult struct {
	Entry    *Entry    `json:"entry"`
	Coverage *Coverage `json:"coverage"`
}

type Service struct {
	yacrDir string
	model   *diffmodel.Model
	commits map[string]string
	nowFn   func() time.Time
}

func OpenService(yacrDir string, model *diffmodel.Model, commitSHAs []string) *Service {
	commits := make(map[string]string, len(commitSHAs))
	for _, sha := range commitSHAs {
		commits[sha] = sha
		if len(sha) >= 7 {
			commits[sha[:7]] = sha
		}
	}
	return &Service{yacrDir: yacrDir, model: model, commits: commits, nowFn: time.Now}
}

func (s *Service) reportPath(targetID string) string {
	return filepath.Join(s.yacrDir, "reports", targetID+".json")
}

func (s *Service) load(targetID string) *Report {
	b, err := os.ReadFile(s.reportPath(targetID))
	if err != nil {
		return &Report{Version: 1, TargetID: targetID}
	}
	var r Report
	if json.Unmarshal(b, &r) != nil {
		return &Report{Version: 1, TargetID: targetID}
	}
	return &r
}

func (s *Service) save(r *Report) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	path := s.reportPath(r.TargetID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Service) Load(targetID string) *Report { return s.load(targetID) }

func (s *Service) Now(now time.Time) { s.nowFn = func() time.Time { return now } }

func (s *Service) Upsert(targetID string, in EntryInput) (out *UpsertResult, verr *ValError, err error) {
	err = withLock(s.reportPath(targetID), func() error {
		var e2 error
		out, verr, e2 = s.upsertLocked(targetID, in)
		return e2
	})
	return out, verr, err
}

func (s *Service) upsertLocked(targetID string, in EntryInput) (*UpsertResult, *ValError, error) {
	r := s.load(targetID)
	now := s.nowFn().UTC().Format(time.RFC3339)

	var target *Entry
	if in.ID != "" {
		for _, e := range r.Entries {
			if e.ID == in.ID {
				target = e
				break
			}
		}
		if target == nil {
			return nil, valErr("not_found", "条目 %s 不存在，无法更新", in.ID), nil
		}
	} else if in.Slug != "" {
		for _, e := range r.Entries {
			if e.Slug != "" && e.Slug == in.Slug {
				target = e
				break
			}
		}
	}

	if strings.TrimSpace(in.Title) == "" {
		return nil, valErr("invalid_entry", "title 不能为空"), nil
	}
	if strings.TrimSpace(in.Explanation) == "" {
		return nil, valErr("invalid_entry", "explanation 不能为空"), nil
	}
	if len(in.Locations) == 0 {
		return nil, valErr("invalid_entry", "locations 不能为空，至少锚定一处变更"), nil
	}

	if _, rerr := s.resolveLocations(in.Locations); rerr != nil {
		return nil, rerr, nil
	}
	commits, cerr := s.resolveCommits(in.Commits)
	if cerr != nil {
		return nil, cerr, nil
	}

	if target == nil {
		maxN := 0
		for _, e := range r.Entries {
			var n int
			fmt.Sscanf(e.ID, "e-%d", &n)
			if n > maxN {
				maxN = n
			}
		}
		target = &Entry{ID: fmt.Sprintf("e-%03d", maxN+1), CreatedAt: now}
		r.Entries = append(r.Entries, target)
	}
	target.Slug = in.Slug
	target.Title = in.Title
	target.Explanation = in.Explanation
	target.Locations = in.Locations
	target.Commits = commits
	target.Tags = in.Tags
	target.UpdatedAt = now

	if err := s.save(r); err != nil {
		return nil, nil, fmt.Errorf("写入报告失败: %w", err)
	}
	return &UpsertResult{Entry: target, Coverage: s.Coverage(targetID)}, nil, nil
}

func (s *Service) Delete(targetID, idOrSlug string) (cov *Coverage, verr *ValError, err error) {
	err = withLock(s.reportPath(targetID), func() error {
		var e2 error
		cov, verr, e2 = s.deleteLocked(targetID, idOrSlug)
		return e2
	})
	return cov, verr, err
}

func (s *Service) deleteLocked(targetID, idOrSlug string) (*Coverage, *ValError, error) {
	r := s.load(targetID)
	idx := -1
	for i, e := range r.Entries {
		if e.ID == idOrSlug || (e.Slug != "" && e.Slug == idOrSlug) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, valErr("not_found", "条目 %s 不存在", idOrSlug), nil
	}
	r.Entries = append(r.Entries[:idx], r.Entries[idx+1:]...)
	if err := s.save(r); err != nil {
		return nil, nil, fmt.Errorf("写入报告失败: %w", err)
	}
	return s.Coverage(targetID), nil, nil
}

func (s *Service) SetSummary(targetID, text string) error {
	return withLock(s.reportPath(targetID), func() error {
		r := s.load(targetID)
		r.Summary = text
		return s.save(r)
	})
}

func (s *Service) resolveLocations(locs []Location) (map[diffmodel.LineRef]bool, *ValError) {
	resolved := map[diffmodel.LineRef]bool{}
	for _, loc := range locs {
		side := diffmodel.Side(loc.Side)

		if loc.Side == "" {
			f := s.model.FileByPath(loc.File, diffmodel.SideNew)
			if f == nil {
				f = s.model.FileByPath(loc.File, diffmodel.SideOld)
			}
			if f == nil {
				return nil, valErr("location_unknown_file", "定位引用了 diff 中不存在的文件 %q", loc.File)
			}
			if f.HasHunks() {
				return nil, valErr("location_file_has_hunks",
					"文件 %s 有行级变更，必须定位到具体行（side=new/old + start/end），不能只给文件", f.Path())
			}
			continue
		}
		if !side.Valid() {
			return nil, valErr("location_invalid_side", "side 必须是 new 或 old，收到 %q", loc.Side)
		}
		f := s.model.FileByPath(loc.File, side)
		if f == nil {
			if of := s.model.FileByPath(loc.File, side.Opposite()); of != nil {
				return nil, valErr("location_side_mismatch",
					"文件 %s 的 %s 侧路径应为 %s", loc.File, side, of.PathFor(side))
			}
			return nil, valErr("location_unknown_file", "定位引用了 diff 中不存在的文件 %q", loc.File)
		}
		if !f.HasHunks() {
			return nil, valErr("location_not_file_level",
				"文件 %s 无行级变更（binary/纯重命名/权限变更），应使用文件级定位（只给 file，不给 side/start/end）", f.Path())
		}
		if loc.Start <= 0 || loc.End < loc.Start {
			return nil, valErr("location_range_invalid", "行范围非法：start=%d end=%d（要求 1 <= start <= end）", loc.Start, loc.End)
		}
		var hit []int
		for ln := loc.Start; ln <= loc.End; ln++ {
			if !f.IsLineInSpan(side, ln) {
				return nil, valErr("location_range_invalid",
					"行 %s:%d(%s) 不在任何 hunk 范围内，变更定位必须落在 diff 变更区域内", f.PathFor(side), ln, side)
			}
			if f.IsChangedLine(side, ln) {
				hit = append(hit, ln)
				resolved[diffmodel.LineRef{FileID: f.ID, Path: f.PathFor(side), Side: side, Line: ln}] = true
			}
		}
		if len(hit) == 0 {
			return nil, valErr("location_no_changed_lines",
				"范围 %s 不包含任何变更行（可能只覆盖了上下文行）", loc.String())
		}
	}
	return resolved, nil
}

func (s *Service) resolveCommits(shas []string) ([]string, *ValError) {
	if len(shas) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(shas))
	seen := map[string]bool{}
	for _, sha := range shas {
		sha = strings.TrimSpace(sha)
		full, ok := s.commits[sha]
		if !ok {
			e := valErr("commit_unknown", "commit %q 不在 review 范围内", sha)
			e.Details = validCommitsDetail(s.commits)
			return nil, e
		}
		if !seen[full] {
			seen[full] = true
			out = append(out, full)
		}
	}
	return out, nil
}

func validCommitsDetail(commits map[string]string) []string {
	var full []string
	seen := map[string]bool{}
	for _, v := range commits {
		if !seen[v] {
			seen[v] = true
			full = append(full, v)
		}
	}
	sort.Strings(full)
	var out []string
	for _, sha := range full {
		out = append(out, sha[:7])
	}
	return out
}

func (s *Service) Coverage(targetID string) *Coverage {
	r := s.load(targetID)
	cov := &Coverage{}
	total := s.model.AllChangedLines()
	cov.TotalLines = len(total)
	covered := map[diffmodel.LineRef]bool{}
	fileUnitCov := map[string]bool{}
	for _, e := range r.Entries {
		for _, loc := range e.Locations {
			side := diffmodel.Side(loc.Side)
			if loc.Side == "" {
				if f := s.model.FileByPath(loc.File, diffmodel.SideNew); f != nil && !f.HasHunks() {
					fileUnitCov[f.ID] = true
				} else if f := s.model.FileByPath(loc.File, diffmodel.SideOld); f != nil && !f.HasHunks() {
					fileUnitCov[f.ID] = true
				}
				continue
			}
			f := s.model.FileByPath(loc.File, side)
			if f == nil {
				continue
			}
			for ln := loc.Start; ln <= loc.End; ln++ {
				ref := diffmodel.LineRef{FileID: f.ID, Path: f.PathFor(side), Side: side, Line: ln}
				if total[ref] != "" {
					covered[ref] = true
				}
			}
		}
	}
	cov.CoveredLines = len(covered)

	byGroup := map[string]*UncoveredGroup{}
	for ref := range total {
		if covered[ref] {
			continue
		}
		key := ref.Path + "\x00" + string(ref.Side)
		g := byGroup[key]
		if g == nil {
			g = &UncoveredGroup{File: ref.Path, Side: string(ref.Side)}
			byGroup[key] = g
		}
		g.Lines = append(g.Lines, ref.Line)
		if h := s.hunkOf(ref); h != "" {
			g.Hunks = appendUnique(g.Hunks, h)
		}
	}
	for _, g := range byGroup {
		sort.Ints(g.Lines)
		cov.Uncovered = append(cov.Uncovered, *g)
	}
	sort.Slice(cov.Uncovered, func(i, j int) bool {
		if cov.Uncovered[i].File != cov.Uncovered[j].File {
			return cov.Uncovered[i].File < cov.Uncovered[j].File
		}
		return cov.Uncovered[i].Side < cov.Uncovered[j].Side
	})

	fileUnits := s.model.FileLevelUnits()
	cov.FileUnitsTotal = len(fileUnits)
	for _, f := range fileUnits {
		if !fileUnitCov[f.ID] {
			cov.UncoveredFiles = append(cov.UncoveredFiles, f.Path())
		} else {
			cov.FileUnitsCovered++
		}
	}
	cov.Complete = cov.TotalLines == cov.CoveredLines && cov.FileUnitsTotal == cov.FileUnitsCovered
	return cov
}

func (s *Service) hunkOf(ref diffmodel.LineRef) string {
	f := s.model.FileByID(ref.FileID)
	if f == nil {
		return ""
	}
	for _, h := range f.Hunks {
		if st, en, ok := h.Span(ref.Side); ok && ref.Line >= st && ref.Line <= en {
			return h.ID
		}
	}
	return ""
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}
