package gitcmd_test

import (
	"strings"
	"testing"

	"yacr/internal/fixture"
)

func TestStatusDetectsTrackedAndUntracked(t *testing.T) {
	r := fixture.Init(t)
	r.Write("a.txt", "hello\n")
	r.Commit("init")
	r.Write("b.txt", "new file\n")

	st, err := r.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.DirtyTracked) != 0 || len(st.Untracked) != 1 || st.Untracked[0] != "b.txt" {
		t.Fatalf("untracked detection failed: %+v", st)
	}
	r.Write("a.txt", "changed\n")
	st, _ = r.Status()
	if len(st.DirtyTracked) != 1 || st.DirtyTracked[0] != "a.txt" {
		t.Fatalf("dirty detection failed: %+v", st)
	}
	if !r.Dirty() {
		t.Fatal("Dirty should be true")
	}
}

func TestCommitsAndFiles(t *testing.T) {
	r := fixture.Init(t)
	r.Write("a.txt", "1\n")
	first := r.Commit("first")
	r.Write("b.txt", "2\n")
	r.Write("c.txt", "3\n")
	second := r.Commit("second: add b and c")

	commits, err := r.Commits(first, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 || commits[0].SHA != second || commits[0].Subject != "second: add b and c" {
		t.Fatalf("commits: %+v", commits)
	}
	files, err := r.CommitFiles(second)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files: %+v", files)
	}
	joined := files[0].Status + files[1].Status
	if joined != "AA" {
		t.Fatalf("statuses: %+v", files)
	}
}

func TestMergeBaseAndAncestor(t *testing.T) {
	r := fixture.Init(t)
	r.Write("base.txt", "0\n")
	base := r.Commit("base")
	r.Write("x.txt", "1\n")
	r.Commit("after")

	if _, err := r.MergeBase(base, "HEAD"); err != nil {
		t.Fatal(err)
	}
	ok, err := r.IsAncestor(base, "HEAD")
	if err != nil || !ok {
		t.Fatalf("base should be ancestor: %v %v", ok, err)
	}
	ok, err = r.IsAncestor("HEAD", base)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("HEAD should not be ancestor of base")
	}
}

func TestDiffUnified(t *testing.T) {
	r := fixture.Init(t)
	r.Write("a.txt", "1\n2\n3\n")
	base := r.Commit("base")
	r.Write("a.txt", "1\nX\n3\n")
	head := r.Commit("change 2")

	out, err := r.DiffUnified(base, head)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "-2") || !strings.Contains(out, "+X") {
		t.Fatalf("diff content: %s", out)
	}
}

func TestBlameLines(t *testing.T) {
	r := fixture.Init(t)
	r.Write("f.txt", "a\n")
	base := r.Commit("base")
	r.Write("f.txt", "a\nb\n")
	head := r.Commit("add b")

	res, err := r.BlameLines(head, "f.txt", [][2]int{{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("blame lines: %+v", res)
	}
	if res[2].SHA != head {
		t.Fatalf("line 2 should be from head: %+v", res[2])
	}
	if res[1].SHA != base || !res[1].Boundary {
		t.Fatalf("line 1 should be boundary base: %+v", res[1])
	}
}

func TestBranch(t *testing.T) {
	r := fixture.Init(t)
	r.Write("a.txt", "x\n")
	r.Commit("init")
	b, err := r.CurrentBranch()
	if err != nil || b != "main" {
		t.Fatalf("branch: %q %v", b, err)
	}
}
