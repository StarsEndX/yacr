package report_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yacr/internal/fixture"
	"yacr/internal/report"
	"yacr/internal/taskgen"
)

type env struct {
	svc      *report.Service
	targetID string
	commits  []string
	yacrDir  string
}

func setup(t *testing.T) *env {
	t.Helper()
	r := fixture.Init(t)
	r.Write("a.txt", "l1\nl2\nl3\nl4\nl5\n")
	r.Write("bin.dat", "\x00\x01binary")
	base := r.Commit("base")
	r.Write("a.txt", "l1\nl2 edited\nl3\nl4\nl5 added\n")
	r.Write("bin.dat", "\x00\x02binary2")
	_ = r.Commit("change")
	yacrDir := filepath.Join(t.TempDir(), "yacr")
	res, err := taskgen.Generate(r.Git, yacrDir, taskgen.Options{ExplicitBase: base})
	if err != nil {
		t.Fatal(err)
	}
	var commits []string
	for _, c := range res.Meta.Commits {
		commits = append(commits, c.SHA)
	}
	return &env{
		svc:      report.OpenService(yacrDir, res.Model, commits),
		targetID: res.Meta.TargetID,
		commits:  commits,
		yacrDir:  yacrDir,
	}
}

func loc(file, side string, start, end int) report.Location {
	return report.Location{File: file, Side: side, Start: start, End: end}
}

func TestUpsertAndCoverage(t *testing.T) {
	e := setup(t)
	in := report.EntryInput{
		Title:       "编辑 l2",
		Explanation: "把 l2 改成 l2 edited",
		Locations:   []report.Location{loc("a.txt", "new", 2, 2), loc("a.txt", "old", 2, 2)},
		Commits:     []string{e.commits[0][:7]},
	}
	res, verr, err := e.svc.Upsert(e.targetID, in)
	if err != nil || verr != nil {
		t.Fatalf("upsert: %v %v", err, verr)
	}
	if res.Entry.ID != "e-001" || len(res.Entry.Commits) != 1 || res.Entry.Commits[0] != e.commits[0] {
		t.Fatalf("entry: %+v", res.Entry)
	}
	cov := res.Coverage
	if cov.TotalLines != 4 || cov.CoveredLines != 2 {
		t.Fatalf("coverage: %+v", cov)
	}
	if cov.FileUnitsTotal != 1 || cov.FileUnitsCovered != 0 || len(cov.UncoveredFiles) != 1 {
		t.Fatalf("file units: %+v", cov)
	}
	if len(cov.Uncovered) != 2 {
		t.Fatalf("uncovered: %+v", cov.Uncovered)
	}
	if cov.Uncovered[0].File != "a.txt" || cov.Uncovered[0].Side != "new" || cov.Uncovered[0].Lines[0] != 5 {
		t.Fatalf("uncovered[0]: %+v", cov.Uncovered[0])
	}

	res, verr, err = e.svc.Upsert(e.targetID, report.EntryInput{
		Title:       "新增 l5",
		Explanation: "追加一行",
		Locations:   []report.Location{loc("a.txt", "new", 5, 5), loc("a.txt", "old", 5, 5)},
	})
	if err != nil || verr != nil {
		t.Fatalf("upsert2: %v %v", err, verr)
	}
	if res.Entry.ID != "e-002" {
		t.Fatalf("id: %s", res.Entry.ID)
	}

	res, verr, err = e.svc.Upsert(e.targetID, report.EntryInput{
		Slug:        "binary-note",
		Title:       "binary 变更",
		Explanation: "二进制文件更新",
		Locations:   []report.Location{{File: "bin.dat"}},
	})
	if err != nil || verr != nil {
		t.Fatalf("upsert3: %v %v", err, verr)
	}
	cov = res.Coverage
	if !cov.Complete {
		t.Fatalf("should be complete: %+v", cov)
	}

	cov, verr, err = e.svc.Delete(e.targetID, "binary-note")
	if err != nil || verr != nil {
		t.Fatalf("delete: %v %v", err, verr)
	}
	if cov.Complete || cov.FileUnitsCovered != 0 {
		t.Fatalf("after delete: %+v", cov)
	}
}

func TestUpsertValidationErrors(t *testing.T) {
	e := setup(t)

	cases := []struct {
		name string
		in   report.EntryInput
		code string
	}{
		{"unknown file", report.EntryInput{
			Title: "x", Explanation: "x",
			Locations: []report.Location{loc("nope.txt", "new", 1, 1)},
		}, "location_unknown_file"},
		{"context only", report.EntryInput{
			Title: "x", Explanation: "x",
			Locations: []report.Location{loc("a.txt", "new", 1, 1)},
		}, "location_no_changed_lines"},
		{"outside span", report.EntryInput{
			Title: "x", Explanation: "x",
			Locations: []report.Location{loc("a.txt", "new", 2, 999)},
		}, "location_range_invalid"},
		{"reversed range", report.EntryInput{
			Title: "x", Explanation: "x",
			Locations: []report.Location{loc("a.txt", "new", 4, 2)},
		}, "location_range_invalid"},
		{"bad side", report.EntryInput{
			Title: "x", Explanation: "x",
			Locations: []report.Location{loc("a.txt", "middle", 2, 2)},
		}, "location_invalid_side"},
		{"unknown commit", report.EntryInput{
			Title: "x", Explanation: "x",
			Locations: []report.Location{loc("a.txt", "new", 2, 2)},
			Commits:   []string{"deadbee"},
		}, "commit_unknown"},
		{"file-level for hunks", report.EntryInput{
			Title: "x", Explanation: "x",
			Locations: []report.Location{{File: "a.txt"}},
		}, "location_file_has_hunks"},
		{"line-level for binary", report.EntryInput{
			Title: "x", Explanation: "x",
			Locations: []report.Location{loc("bin.dat", "new", 1, 1)},
		}, "location_not_file_level"},
		{"empty title", report.EntryInput{
			Title: " ", Explanation: "x",
			Locations: []report.Location{loc("a.txt", "new", 2, 2)},
		}, "invalid_entry"},
		{"no locations", report.EntryInput{
			Title: "x", Explanation: "x",
		}, "invalid_entry"},
	}
	for _, tc := range cases {
		_, verr, err := e.svc.Upsert(e.targetID, tc.in)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.name, err)
		}
		if verr == nil || verr.Code != tc.code {
			t.Fatalf("%s: want code %s, got %v", tc.name, tc.code, verr)
		}
		if e.svc.Load(e.targetID).Summary != "" || len(e.svc.Load(e.targetID).Entries) != 0 {
			t.Fatalf("%s: rejected entry must not be persisted", tc.name)
		}
	}
}

func TestUpdateByIdAndSlug(t *testing.T) {
	e := setup(t)
	in := report.EntryInput{
		Slug:        "l2",
		Title:       "编辑 l2",
		Explanation: "v1",
		Locations:   []report.Location{loc("a.txt", "new", 2, 2)},
	}
	res, verr, err := e.svc.Upsert(e.targetID, in)
	if err != nil || verr != nil {
		t.Fatalf("create: %v %v", err, verr)
	}
	id := res.Entry.ID

	in.Explanation = "v2"
	res, verr, err = e.svc.Upsert(e.targetID, in)
	if err != nil || verr != nil {
		t.Fatalf("slug update: %v %v", err, verr)
	}
	if res.Entry.ID != id || res.Entry.Explanation != "v2" {
		t.Fatalf("slug update result: %+v", res.Entry)
	}
	if len(e.svc.Load(e.targetID).Entries) != 1 {
		t.Fatal("slug upsert should not create duplicate")
	}

	in.ID, in.Slug = id, ""
	in.Explanation = "v3"
	res, verr, err = e.svc.Upsert(e.targetID, in)
	if err != nil || verr != nil {
		t.Fatalf("id update: %v %v", err, verr)
	}
	if res.Entry.Explanation != "v3" {
		t.Fatalf("id update result: %+v", res.Entry)
	}

	_, verr, err = e.svc.Upsert(e.targetID, report.EntryInput{ID: "e-999", Title: "x", Explanation: "x", Locations: []report.Location{loc("a.txt", "new", 2, 2)}})
	if err != nil {
		t.Fatal(err)
	}
	if verr == nil || verr.Code != "not_found" {
		t.Fatalf("unknown id: %v", verr)
	}
}

func TestPersistenceAndSummary(t *testing.T) {
	e := setup(t)
	_, verr, err := e.svc.Upsert(e.targetID, report.EntryInput{
		Title: "x", Explanation: "x",
		Locations: []report.Location{loc("a.txt", "new", 2, 2)},
	})
	if err != nil || verr != nil {
		t.Fatal(err, verr)
	}
	if err := e.svc.SetSummary(e.targetID, "总体说明"); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(e.yacrDir, "reports", e.targetID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted report.Report
	if err := json.Unmarshal(b, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Summary != "总体说明" || len(persisted.Entries) != 1 || persisted.Entries[0].ID != "e-001" {
		t.Fatalf("persisted: %+v", persisted)
	}
	if persisted.Version != 1 || persisted.TargetID != e.targetID {
		t.Fatalf("persisted meta: %+v", persisted)
	}
}

func TestRenameSideMismatch(t *testing.T) {
	r := fixture.Init(t)
	oldContent := ""
	for i := 1; i <= 20; i++ {
		oldContent += strings.Repeat("x", 30) + "\n"
	}
	r.Write("old.txt", oldContent)
	base := r.Commit("base")
	r.Remove("old.txt")
	r.Write("new.txt", oldContent)
	r.Commit("rename")
	yacrDir := filepath.Join(t.TempDir(), "yacr")
	res, err := taskgen.Generate(r.Git, yacrDir, taskgen.Options{ExplicitBase: base})
	if err != nil {
		t.Fatal(err)
	}
	svc := report.OpenService(yacrDir, res.Model, []string{res.Meta.Commits[0].SHA})

	_, verr, err := svc.Upsert(res.Meta.TargetID, report.EntryInput{
		Title: "重命名", Explanation: "x",
		Locations: []report.Location{loc("new.txt", "new", 1, 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if verr != nil && verr.Code != "location_not_file_level" {
		t.Fatalf("rename+line loc should hit file-level requirement or no-change, got: %v", verr)
	}

	_, verr, err = svc.Upsert(res.Meta.TargetID, report.EntryInput{
		Title: "重命名", Explanation: "x",
		Locations: []report.Location{{File: "old.txt"}},
	})
	if err != nil || verr != nil {
		t.Fatalf("file-level by old path should pass: %v %v", err, verr)
	}
	if !svc.Coverage(res.Meta.TargetID).Complete {
		t.Fatal("rename file-level coverage should be complete")
	}
}

func TestCommitPrefixMatching(t *testing.T) {
	e := setup(t)
	full := e.commits[0]
	in := report.EntryInput{
		Title:       "x",
		Explanation: "x",
		Locations:   []report.Location{loc("a.txt", "new", 2, 2)},
	}
	in.Commits = []string{full[:9]}
	if _, verr, _ := e.svc.Upsert(e.targetID, in); verr != nil {
		t.Fatalf("9 位前缀应可匹配: %v", verr)
	}
	in.Commits = []string{full[:3]}
	if _, verr, _ := e.svc.Upsert(e.targetID, in); verr == nil || verr.Code != "commit_unknown" {
		t.Fatalf("过短前缀应拒绝: %v", verr)
	}
}

func TestSlugUniqueness(t *testing.T) {
	e := setup(t)
	in := report.EntryInput{
		Slug: "a", Title: "t1", Explanation: "x",
		Locations: []report.Location{loc("a.txt", "new", 2, 2)},
	}
	if _, verr, _ := e.svc.Upsert(e.targetID, in); verr != nil {
		t.Fatal(verr)
	}
	in2 := report.EntryInput{
		Slug: "b", Title: "t2", Explanation: "x",
		Locations: []report.Location{loc("a.txt", "new", 5, 5), loc("a.txt", "old", 5, 5)},
	}
	res, verr, _ := e.svc.Upsert(e.targetID, in2)
	if verr != nil {
		t.Fatal(verr)
	}
	in2.ID = res.Entry.ID
	in2.Slug = "a"
	if _, verr, _ := e.svc.Upsert(e.targetID, in2); verr == nil || verr.Code != "duplicate_slug" {
		t.Fatalf("slug 冲突应拒绝: %v", verr)
	}
	if len(e.svc.Load(e.targetID).Entries) != 2 {
		t.Fatal("被拒绝的更新不应产生新条目")
	}
}
