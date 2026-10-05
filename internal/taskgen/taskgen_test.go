package taskgen_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"yacr/internal/fixture"
	"yacr/internal/taskgen"
)

func setupAhead(t *testing.T) (*fixture.Repo, string) {
	t.Helper()
	r := fixture.Init(t)
	r.Write("base.txt", "base\n")
	r.Write("a.txt", "a1\na2\na3\n")
	r.Commit("base commit")
	if _, err := r.Run("checkout", "-q", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	r.Write("a.txt", "a1\na2 edited\na3\na4\n")
	r.Commit("feat: edit a")
	r.Write("b.txt", "b1\n")
	r.Commit("feat: add b")
	return r, filepath.Join(t.TempDir(), "yacr")
}

func TestGenerateBundle(t *testing.T) {
	r, yacrDir := setupAhead(t)

	_, err := taskgen.Generate(r.Git, yacrDir, taskgen.Options{})
	if err == nil || !strings.Contains(err.Error(), "不做启发式猜测") {
		t.Fatalf("无 base 时应拒绝: %v", err)
	}

	res, err := taskgen.Generate(r.Git, yacrDir, taskgen.Options{ExplicitBase: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Meta.Branch != "feature" || res.Meta.Incremental {
		t.Fatalf("meta: %+v", res.Meta)
	}
	if res.Meta.BaseRef != "main" {
		t.Fatalf("base ref: %s", res.Meta.BaseRef)
	}
	if len(res.Meta.Commits) != 2 {
		t.Fatalf("commits: %d", len(res.Meta.Commits))
	}
	if res.Meta.Commits[0].Subject != "feat: edit a" {
		t.Fatalf("commit order: %+v", res.Meta.Commits)
	}
	if len(res.Meta.Commits[0].Files) != 1 {
		t.Fatalf("commit files: %+v", res.Meta.Commits[0].Files)
	}
	if res.Stats.Commits != 2 || res.Stats.Files != 2 || res.Stats.ChangedLines != 4 {
		t.Fatalf("stats: %+v", res.Stats)
	}

	cur, err := os.ReadFile(filepath.Join(yacrDir, "current"))
	if err != nil || string(cur) != res.Meta.TargetID {
		t.Fatalf("current pointer: %q %v", cur, err)
	}

	metaBytes, err := os.ReadFile(filepath.Join(res.Dir, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta taskgen.Meta
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.TargetID != res.Meta.TargetID || len(meta.Commits) != 2 {
		t.Fatalf("meta.json: %+v", meta)
	}

	patch, err := os.ReadFile(filepath.Join(res.Dir, "diff.patch"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patch), "a2 edited") {
		t.Fatalf("diff.patch content missing")
	}

	changes, err := os.ReadFile(filepath.Join(res.Dir, "changes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(changes)), "\n")
	if len(lines) != 3 {
		t.Fatalf("changes.jsonl lines: %d\n%s", len(lines), changes)
	}
	type changeRec struct {
		Kind    string   `json:"kind"`
		File    string   `json:"file"`
		Side    string   `json:"side"`
		Hunk    string   `json:"hunk"`
		Lines   []int    `json:"lines"`
		Commits []string `json:"commits"`
	}
	var recs []changeRec
	for _, ln := range lines {
		var rec changeRec
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			t.Fatal(err)
		}
		recs = append(recs, rec)
	}
	if recs[0].Kind != "lines" || recs[0].File != "a.txt" || recs[0].Side != "new" {
		t.Fatalf("rec0: %+v", recs[0])
	}
	if !reflect.DeepEqual(recs[0].Lines, []int{2, 4}) {
		t.Fatalf("rec0 lines: %+v", recs[0])
	}
	if len(recs[0].Commits) != 1 || recs[0].Commits[0] != res.Meta.Commits[0].SHA {
		t.Fatalf("blame attribution: %+v", recs[0])
	}
	if recs[1].Side != "old" || !reflect.DeepEqual(recs[1].Lines, []int{2}) {
		t.Fatalf("rec1: %+v", recs[1])
	}
	if len(recs[1].Commits) != 0 {
		t.Fatalf("old side should have no attribution: %+v", recs[1])
	}
	if recs[2].File != "b.txt" || len(recs[2].Commits) != 1 || recs[2].Commits[0] != res.Meta.Commits[1].SHA {
		t.Fatalf("rec2: %+v", recs[2])
	}
}

func TestGenerateRefusesDirtyWorktree(t *testing.T) {
	r, yacrDir := setupAhead(t)
	r.Write("a.txt", "uncommitted\n")
	_, err := taskgen.Generate(r.Git, yacrDir, taskgen.Options{})
	if err == nil || !strings.Contains(err.Error(), "未提交") {
		t.Fatalf("should refuse dirty: %v", err)
	}
}

func TestGenerateRefusesNoAhead(t *testing.T) {
	r := fixture.Init(t)
	r.Write("a.txt", "x\n")
	r.Commit("only")
	_, err := taskgen.Generate(r.Git, filepath.Join(t.TempDir(), "yacr"), taskgen.Options{ExplicitBase: "main"})
	if err == nil || !strings.Contains(err.Error(), "没有领先提交") {
		t.Fatalf("should refuse empty range: %v", err)
	}
}

func TestResolveBasePriority(t *testing.T) {
	r, yacrDir := setupAhead(t)
	out, err := r.Run("rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSpace(out)

	res, err := taskgen.Generate(r.Git, yacrDir, taskgen.Options{ExplicitBase: base})
	if err != nil {
		t.Fatal(err)
	}
	if res.Meta.BaseRef != base {
		t.Fatalf("explicit base: %s", res.Meta.BaseRef)
	}

	res, err = taskgen.Generate(r.Git, yacrDir, taskgen.Options{Range: base + "..HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Meta.BaseRef, "range ") {
		t.Fatalf("range base: %s", res.Meta.BaseRef)
	}

	head := res.Meta.Head
	if _, err := taskgen.Generate(r.Git, yacrDir, taskgen.Options{
		LastReviewed: func(branch string) (string, bool) { return head, true },
	}); err == nil || !strings.Contains(err.Error(), "没有领先提交") {
		t.Fatalf("last-reviewed == HEAD 时应提示没有新提交: %v", err)
	}

	r.Write("a.txt", "a1\na2 edited\na3\na4\na5\n")
	r.Commit("fix: add a5")
	res, err = taskgen.Generate(r.Git, yacrDir, taskgen.Options{
		LastReviewed: func(branch string) (string, bool) { return head, true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Meta.Incremental || res.Meta.BaseRef != "reviewed-head(feature)" {
		t.Fatalf("incremental: %+v", res.Meta)
	}
	if res.Stats.Commits != 1 {
		t.Fatalf("incremental should cover only 1 commit: %+v", res.Stats)
	}

	if _, err := taskgen.Generate(r.Git, yacrDir, taskgen.Options{
		LastReviewed: func(branch string) (string, bool) { return base, true },
	}); err != nil {
		t.Fatalf("last-reviewed equal to merge-base should still work: %v", err)
	}
}

func TestNoHeuristicBase(t *testing.T) {
	r := fixture.Init(t)
	r.Write("a.txt", "base\n")
	r.Commit("base")
	if _, err := r.Run("branch", "-M", "develop"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run("checkout", "-q", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	r.Write("a.txt", "base\nfeat\n")
	r.Commit("feat commit")
	yacrDir := filepath.Join(t.TempDir(), "yacr")

	_, err := taskgen.Generate(r.Git, yacrDir, taskgen.Options{})
	if err == nil {
		t.Fatal("无任何确认来源时应拒绝")
	}
	msg := err.Error()
	for _, want := range []string{"develop", "候选分支", "yacr config base"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("拒绝信息应含 %q:\n%s", want, msg)
		}
	}

	res, err := taskgen.Generate(r.Git, yacrDir, taskgen.Options{ConfigBase: "develop"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Meta.BaseRef != "develop (config)" {
		t.Fatalf("config base: %s", res.Meta.BaseRef)
	}
	if res.Meta.Branch != "feature" {
		t.Fatalf("branch: %s", res.Meta.Branch)
	}

	_, err = taskgen.Generate(r.Git, yacrDir, taskgen.Options{ConfigBase: "no-such-ref"})
	if err == nil || !strings.Contains(err.Error(), "无法解析") {
		t.Fatalf("坏 config base 应报错: %v", err)
	}
}

func TestBaseCandidatesOrder(t *testing.T) {
	r := fixture.Init(t)
	r.Write("a.txt", "base\n")
	r.Commit("base")
	if _, err := r.Run("branch", "-M", "master"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run("branch", "version/1.2"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run("checkout", "-q", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	r.Write("a.txt", "base\nfeat\n")
	r.Commit("feat commit")

	cands := taskgen.BaseCandidates(r.Git, 8)
	if len(cands) < 2 {
		t.Fatalf("candidates: %+v", cands)
	}
	if !cands[0].Contained || cands[0].Ref != "master" {
		t.Fatalf("首个候选应为已完全合入的同步源: %+v", cands[0])
	}
	for _, c := range cands {
		if c.Ahead == 0 {
			t.Fatalf("ahead=0 不应入选: %+v", c)
		}
	}
}

func TestGenerateFileUnitsAndBinary(t *testing.T) {
	r := fixture.Init(t)
	r.Write("s.txt", "x\n")
	base := r.Commit("base")
	r.Chmod("s.txt", 0o755)
	r.Write("bin.dat", "\x00\x01\x02zz")
	r.Commit("mode+binary")

	res, err := taskgen.Generate(r.Git, filepath.Join(t.TempDir(), "yacr"), taskgen.Options{ExplicitBase: base})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.FileUnits != 2 || res.Stats.ChangedLines != 0 {
		t.Fatalf("stats: %+v", res.Stats)
	}
	changes, err := os.ReadFile(filepath.Join(res.Dir, "changes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(changes), `"kind":"file"`) {
		t.Fatalf("changes.jsonl should contain file units:\n%s", changes)
	}
	if len(res.Model.FileLevelUnits()) != 2 {
		t.Fatal("file units")
	}
}
