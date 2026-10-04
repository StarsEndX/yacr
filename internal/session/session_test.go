package session_test

import (
	"os"
	"path/filepath"
	"testing"

	"yacr/internal/session"
)

func TestMarkAndFind(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "yacr")
	rec := session.Record{
		TargetID: "abc1234..def5678",
		Branch:   "feature/x",
		Base:     "abc1234",
		Head:     "def5678",
		DoneAt:   "2026-10-05T00:00:00Z",
	}
	if err := session.MarkDone(dir, rec); err != nil {
		t.Fatal(err)
	}
	got := session.Find(dir, rec.TargetID)
	if got == nil || got.Head != rec.Head || got.Branch != rec.Branch {
		t.Fatalf("find: %+v", got)
	}

	sha, ok := session.LastReviewedHead(dir, "feature/x")
	if !ok || sha != "def5678" {
		t.Fatalf("last reviewed: %q %v", sha, ok)
	}
	if _, ok := session.LastReviewedHead(dir, "other"); ok {
		t.Fatal("其他分支不应有记录")
	}

	rec2 := rec
	rec2.TargetID = "def5678..aaa1111"
	rec2.Head = "aaa1111"
	rec2.DoneAt = "2026-10-06T00:00:00Z"
	if err := session.MarkDone(dir, rec2); err != nil {
		t.Fatal(err)
	}
	sha, _ = session.LastReviewedHead(dir, "feature/x")
	if sha != "aaa1111" {
		t.Fatalf("应取最新记录: %q", sha)
	}
}

func TestSanitize(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "yacr")
	rec := session.Record{TargetID: "a..b/c d", Branch: "x", Head: "h", DoneAt: "t"}
	if err := session.MarkDone(dir, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", "a..b_c_d.json")); err != nil {
		t.Fatalf("target id 应被净化为安全文件名: %v", err)
	}
}
