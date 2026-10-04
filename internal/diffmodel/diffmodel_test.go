package diffmodel_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"yacr/internal/diffmodel"
	"yacr/internal/fixture"
)

func parseHeadDiff(t *testing.T, r *fixture.Repo, base string) *diffmodel.Model {
	t.Helper()
	out, err := r.DiffUnified(base, r.Head())
	if err != nil {
		t.Fatal(err)
	}
	m, err := diffmodel.Parse(out)
	if err != nil {
		t.Fatalf("解析失败: %v\n%s", err, out)
	}
	return m
}

func TestModifyMultiHunk(t *testing.T) {
	r := fixture.Init(t)
	content := ""
	for i := 1; i <= 30; i++ {
		content += line(i)
	}
	r.Write("a.txt", content)
	base := r.Commit("base")
	next := ""
	for i := 1; i <= 30; i++ {
		switch i {
		case 3:
			next += "three\n"
		case 14:
			next += "fourteen\n"
		default:
			next += line(i)
		}
	}
	r.Write("a.txt", next)
	r.Commit("change 3 and 14")

	m := parseHeadDiff(t, r, base)
	if len(m.Files) != 1 {
		t.Fatalf("files: %d", len(m.Files))
	}
	f := m.Files[0]
	if f.ID != "F01" || f.Status != diffmodel.StatusModified || f.Path() != "a.txt" {
		t.Fatalf("file: %+v", f)
	}
	if len(f.Hunks) != 2 {
		t.Fatalf("hunks: %d", len(f.Hunks))
	}
	h1, h2 := f.Hunks[0], f.Hunks[1]
	if h1.ID != "F01.H01" || h2.ID != "F01.H02" {
		t.Fatalf("hunk ids: %s %s", h1.ID, h2.ID)
	}
	if !reflect.DeepEqual(f.ChangedLines(diffmodel.SideNew), []int{3, 14}) {
		t.Fatalf("new changed: %v", f.ChangedLines(diffmodel.SideNew))
	}
	if !reflect.DeepEqual(f.ChangedLines(diffmodel.SideOld), []int{3, 14}) {
		t.Fatalf("old changed: %v", f.ChangedLines(diffmodel.SideOld))
	}
	all := m.AllChangedLines()
	if len(all) != 4 {
		t.Fatalf("all changed: %d", len(all))
	}
	if _, ok := all[diffmodel.LineRef{"F01", "a.txt", diffmodel.SideNew, 3}]; !ok {
		t.Fatalf("missing ref new:3")
	}
	if _, ok := all[diffmodel.LineRef{"F01", "a.txt", diffmodel.SideOld, 14}]; !ok {
		t.Fatalf("missing ref old:14")
	}
}

func TestAddAndDelete(t *testing.T) {
	r := fixture.Init(t)
	r.Write("keep.txt", "k\n")
	r.Write("del.txt", "d1\nd2\n")
	base := r.Commit("base")
	r.Remove("del.txt")
	r.Write("new.txt", "n1\nn2\nn3\n")
	r.Commit("delete + add")

	m := parseHeadDiff(t, r, base)
	if len(m.Files) != 2 {
		t.Fatalf("files: %d", len(m.Files))
	}
	del := m.FileByPath("del.txt", diffmodel.SideOld)
	if del == nil || del.Status != diffmodel.StatusDeleted {
		t.Fatalf("del: %+v", del)
	}
	if !reflect.DeepEqual(del.ChangedLines(diffmodel.SideOld), []int{1, 2}) {
		t.Fatalf("del old lines: %v", del.ChangedLines(diffmodel.SideOld))
	}
	add := m.FileByPath("new.txt", diffmodel.SideNew)
	if add == nil || add.Status != diffmodel.StatusAdded {
		t.Fatalf("add: %+v", add)
	}
	if !reflect.DeepEqual(add.ChangedLines(diffmodel.SideNew), []int{1, 2, 3}) {
		t.Fatalf("add new lines: %v", add.ChangedLines(diffmodel.SideNew))
	}
	if add.ChangedLines(diffmodel.SideOld) != nil {
		t.Fatalf("add should have no old lines")
	}
}

func TestRenamePureAndWithEdit(t *testing.T) {
	r := fixture.Init(t)
	old := ""
	for i := 1; i <= 20; i++ {
		old += fmt.Sprintf("line %02d: some longer content here\n", i)
	}
	r.Write("old.txt", old)
	base := r.Commit("base")
	r.Remove("old.txt")
	r.Write("moved.txt", old)
	r.Commit("pure rename")

	m := parseHeadDiff(t, r, base)
	if len(m.Files) != 1 {
		t.Fatalf("files: %d", len(m.Files))
	}
	f := m.Files[0]
	if f.Status != diffmodel.StatusRenamed || f.OldPath != "old.txt" || f.NewPath != "moved.txt" {
		t.Fatalf("rename: %+v", f)
	}
	if f.HasHunks() {
		t.Fatalf("pure rename should have no hunks")
	}

	edited := strings.Replace(old, "line 05: some longer content here", "line 05: edited content", 1)
	r.Write("moved.txt", edited)
	r.Commit("edit after rename")
	m2 := parseHeadDiff(t, r, base)
	if len(m2.Files) != 1 || !m2.Files[0].HasHunks() {
		t.Fatalf("rename+edit: %+v", m2.Files)
	}
	f2 := m2.Files[0]
	if f2.Status != diffmodel.StatusRenamed {
		t.Fatalf("status: %s", f2.Status)
	}
	if !reflect.DeepEqual(f2.ChangedLines(diffmodel.SideOld), []int{5}) {
		t.Fatalf("old: %v", f2.ChangedLines(diffmodel.SideOld))
	}
	if !reflect.DeepEqual(f2.ChangedLines(diffmodel.SideNew), []int{5}) {
		t.Fatalf("new: %v", f2.ChangedLines(diffmodel.SideNew))
	}
}

func TestModeOnlyAndBinary(t *testing.T) {
	r := fixture.Init(t)
	r.Write("run.sh", "echo hi\n")
	r.Chmod("run.sh", 0o644)
	r.Write("bin.dat", "\x00\x01\x02binary")
	base := r.Commit("base")
	r.Chmod("run.sh", 0o755)
	r.Write("bin.dat", "\x00\x01\x03binary2")
	r.Commit("mode + binary")

	m := parseHeadDiff(t, r, base)
	if len(m.Files) != 2 {
		t.Fatalf("files: %d", len(m.Files))
	}
	sh := m.FileByPath("run.sh", diffmodel.SideNew)
	if sh == nil || sh.HasHunks() || sh.OldMode != "100644" || sh.NewMode != "100755" {
		t.Fatalf("mode change: %+v", sh)
	}
	bin := m.FileByPath("bin.dat", diffmodel.SideNew)
	if bin == nil || !bin.Binary || bin.HasHunks() {
		t.Fatalf("binary: %+v", bin)
	}
	if len(m.FileLevelUnits()) != 2 {
		t.Fatalf("file units: %d", len(m.FileLevelUnits()))
	}
}

func TestNoNewlineAtEnd(t *testing.T) {
	r := fixture.Init(t)
	r.Write("f.txt", "a\nb")
	base := r.Commit("base")
	r.Write("f.txt", "a\nb\nc")
	r.Commit("no newline")

	m := parseHeadDiff(t, r, base)
	f := m.FileByPath("f.txt", diffmodel.SideNew)
	if f == nil || len(f.Hunks) != 1 {
		t.Fatalf("file: %+v", f)
	}
	if !reflect.DeepEqual(f.ChangedLines(diffmodel.SideOld), []int{2}) {
		t.Fatalf("old: %v", f.ChangedLines(diffmodel.SideOld))
	}
	if !reflect.DeepEqual(f.ChangedLines(diffmodel.SideNew), []int{2, 3}) {
		t.Fatalf("new: %v", f.ChangedLines(diffmodel.SideNew))
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := diffmodel.Parse("hello world\n"); err == nil {
		t.Fatal("should reject non-diff input")
	}
	if _, err := diffmodel.Parse("diff --git a/x b/x\n@@ -bad hunk\n"); err == nil {
		t.Fatal("should reject bad hunk header")
	}
	if _, err := diffmodel.Parse(""); err == nil {
		t.Fatal("should reject empty")
	}
}

func line(n int) string {
	switch n {
	case 1:
		return "one\n"
	case 2:
		return "two\n"
	case 3:
		return "three-orig\n"
	}
	if n < 10 {
		return string(rune('0'+n)) + "\n"
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10)) + "\n"
}
