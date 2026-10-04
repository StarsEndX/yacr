package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yacr/internal/app"
	"yacr/internal/diffmodel"
	"yacr/internal/fixture"
	"yacr/internal/report"
	"yacr/internal/taskgen"
)

func setupCtx(t *testing.T) *app.Ctx {
	t.Helper()
	r := fixture.Init(t)
	r.Write(".gitignore", ".yacr/\n")
	r.Write("f.go", "a\nb\nc\nd\n")
	base := r.Commit("base")
	r.Write("f.go", "a\nB\nc\nD\n")
	r.Commit("change b and d")

	res, err := taskgen.Generate(r.Git, filepath.Join(t.TempDir(), "yacr"), taskgen.Options{ExplicitBase: base})
	if err != nil {
		t.Fatal(err)
	}
	commits := []string{res.Meta.Commits[0].SHA}
	yacrDir := filepath.Join(t.TempDir(), "yacr")
	if err := os.MkdirAll(yacrDir, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := report.OpenService(yacrDir, res.Model, commits)
	if _, verr, err := svc.Upsert(res.Meta.TargetID, report.EntryInput{
		Title:       "改 b",
		Explanation: "把 b 换成 B",
		Locations:   []report.Location{{File: "f.go", Side: "new", Start: 2, End: 2}},
	}); err != nil || verr != nil {
		t.Fatal(err, verr)
	}
	return &app.Ctx{
		YacrDir: yacrDir,
		Meta:    res.Meta,
		Model:   res.Model,
		Service: svc,
	}
}

func TestBuildOverviewRows(t *testing.T) {
	ctx := setupCtx(t)
	cov, files := ctx.Overview()
	rows := buildOverviewRows(cov, files, ctx.Entries())
	if len(rows) != 3 {
		t.Fatalf("rows: %d（应有 1 未解释 new + 1 未解释 old + 1 条目）: %+v", len(rows), rows)
	}
	if rows[0].kind != "uncovered" || !strings.Contains(rows[0].text, "new") {
		t.Fatalf("row0: %+v", rows[0])
	}
	if rows[2].kind != "entry" || !strings.Contains(rows[2].text, "改 b") {
		t.Fatalf("row2: %+v", rows[2])
	}
}

func TestRenderDiffFileMarks(t *testing.T) {
	ctx := setupCtx(t)
	f := ctx.Model.FileByPath("f.go", diffmodel.SideNew)
	lines := renderDiffFile(ctx, f, 100)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "●") || !strings.Contains(joined, "○") {
		t.Fatalf("diff 渲染应同时含已覆盖●与未覆盖○:\n%s", joined)
	}
	if !strings.Contains(joined, "← e-001") {
		t.Fatalf("已覆盖行应显示条目 id:\n%s", joined)
	}
	if !strings.Contains(joined, "F01.H01") {
		t.Fatalf("缺少 hunk id:\n%s", joined)
	}
}

func TestWrapText(t *testing.T) {
	out := wrapText("aaa bbb ccc ddd", 7, "  ")
	if len(out) != 2 {
		t.Fatalf("wrap: %v", out)
	}
	if out[0] != "  aaa bbb" {
		t.Fatalf("wrap[0]: %q", out[0])
	}
}
