package gitcmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type Commit struct {
	SHA     string
	Subject string
	Body    string
}

type FileChange struct {
	Status  string
	Path    string
	OldPath string
}

type Git struct {
	dir      string
	extraEnv []string
}

func New(dir string) *Git {
	return &Git{dir: dir}
}

func (g *Git) Dir() string { return g.dir }

func (g *Git) WithEnv(env ...string) *Git {
	return &Git{dir: g.dir, extraEnv: append(append([]string{}, g.extraEnv...), env...)}
}

func (g *Git) Run(args ...string) (string, error) {
	full := append([]string{"-C", g.dir, "-c", "core.quotepath=false", "--no-pager"}, args...)
	cmd := exec.Command("git", full...)
	env := append([]string{
		"LC_ALL=C", "LANG=C", "GIT_PAGER=cat", "PAGER=cat", "TERM=dumb",
	}, os.Environ()...)
	env = append(env, g.extraEnv...)
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return out.String(), nil
}

func (g *Git) TopLevel() (string, error) {
	out, err := g.Run("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (g *Git) IsInsideRepo() bool {
	_, err := g.TopLevel()
	return err == nil
}

type WorktreeStatus struct {
	DirtyTracked []string
	Untracked    []string
}

func (g *Git) Status() (*WorktreeStatus, error) {
	out, err := g.Run("status", "--porcelain")
	if err != nil {
		return nil, err
	}
	st := &WorktreeStatus{}
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if len(ln) < 4 {
			continue
		}
		code, path := ln[:2], ln[3:]
		if strings.HasPrefix(path, "\"") && strings.HasSuffix(path, "\"") {
			path = strings.Trim(path, "\"")
		}
		if code == "!!" {
			continue
		}
		if code == "??" {
			st.Untracked = append(st.Untracked, path)
		} else {
			st.DirtyTracked = append(st.DirtyTracked, path)
		}
	}
	return st, nil
}

func (g *Git) CurrentBranch() (string, error) {
	out, err := g.Run("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (g *Git) ResolveCommit(ref string) (string, error) {
	out, err := g.Run("rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("无法解析引用 %s: %w", ref, err)
	}
	return strings.TrimSpace(out), nil
}

func (g *Git) HasRef(ref string) bool {
	_, err := g.ResolveCommit(ref)
	return err == nil
}

func (g *Git) MergeBase(a, b string) (string, error) {
	out, err := g.Run("merge-base", a, b)
	if err != nil {
		return "", fmt.Errorf("计算 merge-base(%s, %s) 失败: %w", a, b, err)
	}
	return strings.TrimSpace(out), nil
}

func (g *Git) IsAncestor(a, b string) (bool, error) {
	_, err := g.Run("merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return false, nil
	}
	return false, err
}

func (g *Git) Commits(base, head string) ([]Commit, error) {
	out, err := g.Run("log", "--reverse", "--format=%H%x1f%s%x1f%b%x1e", base+".."+head)
	if err != nil {
		return nil, fmt.Errorf("读取提交列表失败: %w", err)
	}
	var commits []Commit
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		if strings.TrimSpace(rec) == "" {
			continue
		}
		parts := strings.SplitN(rec, "\x1f", 3)
		c := Commit{SHA: strings.TrimSpace(parts[0])}
		if len(parts) > 1 {
			c.Subject = strings.TrimRight(parts[1], "\n")
		}
		if len(parts) > 2 {
			c.Body = strings.Trim(parts[2], "\n")
		}
		commits = append(commits, c)
	}
	return commits, nil
}

var nameStatusRe = regexp.MustCompile(`^([ADCRTUXB])(\d+)?\t(.*)$`)

func (g *Git) CommitFiles(sha string) ([]FileChange, error) {
	out, err := g.Run("diff-tree", "--no-commit-id", "--name-status", "-r", "-M", "--root", sha)
	if err != nil {
		return nil, fmt.Errorf("读取提交 %s 的文件列表失败: %w", Short(sha), err)
	}
	var files []FileChange
	for _, ln := range strings.Split(out, "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		fields := strings.SplitN(ln, "\t", 3)
		if len(fields) == 2 {
			files = append(files, FileChange{Status: fields[0], Path: fields[1]})
		} else if len(fields) == 3 {
			files = append(files, FileChange{Status: fields[0], OldPath: fields[1], Path: fields[2]})
		}
	}
	return files, nil
}

func (g *Git) DiffUnified(base, head string) (string, error) {
	out, err := g.Run("diff", "--find-renames", "--no-color", "--no-ext-diff", "--src-prefix=a/", "--dst-prefix=b/", base, head)
	if err != nil {
		return "", fmt.Errorf("生成 diff 失败: %w", err)
	}
	return out, nil
}

type BlameLine struct {
	SHA      string
	Boundary bool
}

var blameEntryRe = regexp.MustCompile(`^([0-9a-f]{40}) (\d+) (\d+)(?: (\d+))?`)

func (g *Git) BlameLines(rev, file string, ranges [][2]int) (map[int]BlameLine, error) {
	args := []string{"blame", "--line-porcelain"}
	for _, r := range ranges {
		args = append(args, "-L", fmt.Sprintf("%d,%d", r[0], r[1]))
	}
	args = append(args, rev, "--", file)
	out, err := g.Run(args...)
	if err != nil {
		return nil, fmt.Errorf("blame %s:%s 失败: %w", Short(rev), file, err)
	}
	res := make(map[int]BlameLine)
	curFinal := -1
	inHeader := false
	for _, ln := range strings.Split(out, "\n") {
		if m := blameEntryRe.FindStringSubmatch(ln); m != nil {
			final, _ := strconv.Atoi(m[3])
			curFinal = final
			res[final] = BlameLine{SHA: m[1]}
			inHeader = true
			continue
		}
		if !inHeader {
			continue
		}
		if strings.HasPrefix(ln, "boundary") {
			bl := res[curFinal]
			bl.Boundary = true
			res[curFinal] = bl
		} else if strings.HasPrefix(ln, "\t") {
			inHeader = false
		}
	}
	return res, nil
}

func Short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
