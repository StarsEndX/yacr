package fixture

import (
	"os"
	"path/filepath"
	"testing"

	"yacr/internal/gitcmd"
)

const env = "GIT_AUTHOR_NAME=yacr-test GIT_COMMITTER_NAME=yacr-test"

func newGit(t testing.TB, dir string) *gitcmd.Git {
	t.Helper()
	g := gitcmd.New(dir).WithEnv(
		"GIT_AUTHOR_NAME=yacr-test",
		"GIT_AUTHOR_EMAIL=test@yacr.local",
		"GIT_COMMITTER_NAME=yacr-test",
		"GIT_COMMITTER_EMAIL=test@yacr.local",
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00+00:00",
		"GIT_COMMITTER_DATE=2026-01-01T00:00:00+00:00",
	)
	return g
}

func Init(t testing.TB) *Repo {
	t.Helper()
	dir := t.TempDir()
	g := newGit(t, dir)
	if _, err := g.Run("init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	return &Repo{T: t, Git: g, Dir: dir}
}

type Repo struct {
	T testing.TB
	*gitcmd.Git
	Dir string
}

func (r *Repo) Write(path, content string) {
	r.T.Helper()
	full := filepath.Join(r.Dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.T.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.T.Fatalf("write: %v", err)
	}
}

func (r *Repo) Remove(path string) {
	r.T.Helper()
	full := filepath.Join(r.Dir, filepath.FromSlash(path))
	if err := os.Remove(full); err != nil {
		r.T.Fatalf("remove: %v", err)
	}
}

func (r *Repo) Chmod(path string, mode os.FileMode) {
	r.T.Helper()
	full := filepath.Join(r.Dir, filepath.FromSlash(path))
	if err := os.Chmod(full, mode); err != nil {
		r.T.Fatalf("chmod: %v", err)
	}
}

func (r *Repo) Commit(msg string) string {
	r.T.Helper()
	if _, err := r.Run("add", "-A"); err != nil {
		r.T.Fatalf("git add: %v", err)
	}
	if _, err := r.Run("commit", "--quiet", "-m", msg); err != nil {
		r.T.Fatalf("git commit(%s): %v", msg, err)
	}
	sha, err := r.ResolveCommit("HEAD")
	if err != nil {
		r.T.Fatalf("resolve HEAD: %v", err)
	}
	return sha
}

func (r *Repo) Head() string {
	r.T.Helper()
	sha, err := r.ResolveCommit("HEAD")
	if err != nil {
		r.T.Fatalf("resolve HEAD: %v", err)
	}
	return sha
}

func (r *Repo) Dirty() bool {
	r.T.Helper()
	st, err := r.Status()
	if err != nil {
		r.T.Fatalf("status: %v", err)
	}
	return len(st.DirtyTracked) > 0
}
