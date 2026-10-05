package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"yacr/internal/fixture"
)

var binaryPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "yacr-bin")
	if err != nil {
		panic(err)
	}
	binaryPath = filepath.Join(dir, "yacr")
	cmd := exec.Command("go", "build", "-o", binaryPath, ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type cliResult struct {
	stdout string
	stderr string
	code   int
}

func runYacr(t *testing.T, repoDir string, stdin string, args ...string) cliResult {
	t.Helper()
	cmd := exec.Command(binaryPath, append(args, "--repo", repoDir)...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), "TERM=dumb", "NO_COLOR=1")
	err := cmd.Run()
	res := cliResult{stdout: out.String(), stderr: errb.String()}
	if ee, ok := err.(*exec.ExitError); ok {
		res.code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("运行 yacr 失败: %v", err)
	}
	return res
}

func setupRepo(t *testing.T) *fixture.Repo {
	t.Helper()
	r := fixture.Init(t)
	r.Write(".gitignore", ".yacr/\n")
	r.Write("app.go", "package main\n\nfunc main() {}\n")
	r.Commit("base")
	if _, err := r.Run("checkout", "-q", "-b", "feature/auth"); err != nil {
		t.Fatal(err)
	}
	r.Write("app.go", "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(user()) }\n\nfunc user() string { return \"anon\" }\n")
	r.Commit("feat: greet user")
	r.Write("auth.go", "package main\n\nfunc login(u string) bool { return u != \"\" }\n")
	r.Write("app.go", "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(user()) }\n\nfunc user() string { return \"anon\" }\n\nfunc extra() { _ = login(\"x\") }\n")
	r.Commit("feat: add auth")
	return r
}

func upsertJSON(slug, title, explanation string, locs []map[string]any, commits []string) string {
	body := map[string]any{
		"slug": slug, "title": title, "explanation": explanation,
		"locations": locs,
	}
	if commits != nil {
		body["commits"] = commits
	}
	b, _ := json.Marshal(body)
	return string(b)
}

func loc(file, side string, start, end int) map[string]any {
	return map[string]any{"file": file, "side": side, "start": start, "end": end}
}

func TestE2EHappyPath(t *testing.T) {
	r := setupRepo(t)

	res := runYacr(t, r.Dir, "", "task")
	if res.code != 0 {
		t.Fatalf("task: %s\n%s", res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, "review 任务已生成") || !strings.Contains(res.stdout, "feature/auth") {
		t.Fatalf("task output: %s", res.stdout)
	}

	res = runYacr(t, r.Dir, "", "show", "--json")
	if res.code != 0 {
		t.Fatalf("show: %s\n%s", res.stdout, res.stderr)
	}
	var overview struct {
		TargetID string `json:"target_id"`
		Coverage struct {
			TotalLines   int  `json:"total_lines"`
			CoveredLines int  `json:"covered_lines"`
			Complete     bool `json:"complete"`
		} `json:"coverage"`
		Files []struct {
			Path string `json:"path"`
			ID   string `json:"id"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &overview); err != nil {
		t.Fatalf("show json 解析失败: %v\n%s", err, res.stdout)
	}
	if overview.Coverage.Complete || overview.Coverage.TotalLines == 0 {
		t.Fatalf("初始覆盖率应为 0: %+v", overview.Coverage)
	}
	if len(overview.Files) != 2 {
		t.Fatalf("files: %+v", overview.Files)
	}

	res = runYacr(t, r.Dir, "", "validate")
	if res.code != 2 {
		t.Fatalf("未覆盖时 validate 应 exit 2, got %d\n%s", res.code, res.stdout)
	}

	res = runYacr(t, r.Dir, "", "report", "upsert",
		"--slug", "greet-user",
		"--title", "增加用户问候",
		"--explanation", "main 输出 user() 结果，新增 user() 返回匿名用户",
		"--commit", "HEAD~1",
	)
	if res.code != 2 {
		t.Fatalf("非范围 sha 的 commit 应被拒绝(exit 2), got %d\n%s\n%s", res.code, res.stdout, res.stderr)
	}

	headOut, err := r.Run("rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	head7 := strings.TrimSpace(headOut)[:7]

	res = runYacr(t, r.Dir, "", "report", "upsert",
		"--slug", "greet-user",
		"--title", "增加用户问候",
		"--explanation", "main 输出 user() 结果，新增 user() 返回匿名用户",
		"--loc", "app.go:3-7:new",
		"--commit", head7,
	)
	if res.code != 0 {
		t.Fatalf("upsert: %s\n%s", res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, "已保存条目 e-001") {
		t.Fatalf("upsert output: %s", res.stdout)
	}

	res = runYacr(t, r.Dir, upsertJSON("auth",
		"新增登录校验",
		"login 判断用户名非空；extra() 演示调用；替换旧的空 main。",
		[]map[string]any{
			loc("app.go", "new", 8, 9),
			loc("app.go", "old", 3, 3),
			loc("auth.go", "new", 1, 3),
		},
		[]string{head7},
	), "report", "upsert", "--file", "-")
	if res.code != 0 {
		t.Fatalf("upsert2: %s\n%s", res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, "全部覆盖") {
		t.Fatalf("upsert2 output: %s", res.stdout)
	}

	res = runYacr(t, r.Dir, "", "validate")
	if res.code != 0 || !strings.Contains(res.stdout, "完整 ✓") {
		t.Fatalf("validate: code=%d\n%s\n%s", res.code, res.stdout, res.stderr)
	}

	res = runYacr(t, r.Dir, "", "show", "app.go:9")
	if res.code != 0 {
		t.Fatalf("show loc: %s\n%s", res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, "新增登录校验") && !strings.Contains(res.stdout, "增加用户问候") {
		t.Fatalf("show loc output: %s", res.stdout)
	}
	if !strings.Contains(res.stdout, "●") {
		t.Fatalf("hunk 渲染缺少覆盖标记: %s", res.stdout)
	}

	res = runYacr(t, r.Dir, "", "show", "app.go:9", "--json")
	if res.code != 0 {
		t.Fatalf("show loc json: %s\n%s", res.stdout, res.stderr)
	}
	var q map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &q); err != nil {
		t.Fatal(err)
	}
	if q["covered"] != true {
		t.Fatalf("query json: %v", q)
	}

	res = runYacr(t, r.Dir, "", "feedback")
	if res.code != 0 || !strings.Contains(res.stdout, "# Review 摘要") || !strings.Contains(res.stdout, "新增登录校验") {
		t.Fatalf("feedback: %s\n%s", res.stdout, res.stderr)
	}

	res = runYacr(t, r.Dir, "", "report", "summary", "本次变更实现问候与登录校验")
	if res.code != 0 {
		t.Fatalf("summary: %s\n%s", res.stdout, res.stderr)
	}

	res = runYacr(t, r.Dir, "", "done")
	if res.code != 0 {
		t.Fatalf("done: %s\n%s", res.stdout, res.stderr)
	}

	r.Write("auth.go", "package main\n\nfunc login(u string) bool { return len(u) > 0 }\n\nfunc logout() {}\n")
	r.Commit("fix: strict login and add logout")
	res = runYacr(t, r.Dir, "", "task")
	if res.code != 0 {
		t.Fatalf("incremental task: %s\n%s", res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, "增量") {
		t.Fatalf("应识别增量: %s", res.stdout)
	}
	if !strings.Contains(res.stdout, "提交: 1 个") {
		t.Fatalf("增量只含 1 个提交: %s", res.stdout)
	}
}

func TestE2EValidationErrors(t *testing.T) {
	r := setupRepo(t)
	if res := runYacr(t, r.Dir, "", "task"); res.code != 0 {
		t.Fatalf("task: %s", res.stderr)
	}

	cases := []struct {
		name     string
		args     []string
		stdin    string
		wantCode string
	}{
		{"context-only loc", []string{"report", "upsert", "--title", "x", "--explanation", "x", "--loc", "app.go:1:new"}, "", "location_no_changed_lines"},
		{"unknown file", []string{"report", "upsert", "--title", "x", "--explanation", "x", "--loc", "nope.go:1:new"}, "", "location_unknown_file"},
	}
	for _, tc := range cases {
		res := runYacr(t, r.Dir, tc.stdin, tc.args...)
		if res.code != 2 {
			t.Fatalf("%s: want exit 2, got %d\n%s\n%s", tc.name, res.code, res.stdout, res.stderr)
		}
		if !strings.Contains(res.stderr, tc.wantCode) {
			t.Fatalf("%s: stderr 应含 %s:\n%s", tc.name, tc.wantCode, res.stderr)
		}
	}

	res := runYacr(t, r.Dir, "", "report", "delete", "e-001")
	if res.code != 2 {
		t.Fatalf("删除不存在的条目应 exit 2, got %d", res.code)
	}

	res = runYacr(t, r.Dir, "", "show", "app.go:1")
	if res.code != 0 || !strings.Contains(res.stdout, "上下文行") {
		t.Fatalf("show 上下文行: %s\n%s", res.stdout, res.stderr)
	}
	res = runYacr(t, r.Dir, "", "show", "app.go:20")
	if res.code != 0 || !strings.Contains(res.stdout, "不在变更区域内") {
		t.Fatalf("show 范围外行: %s\n%s", res.stdout, res.stderr)
	}
}

func TestE2EDirtyWorktree(t *testing.T) {
	r := setupRepo(t)
	r.Write("app.go", "dirty\n")
	res := runYacr(t, r.Dir, "", "task")
	if res.code != 1 || !strings.Contains(res.stderr, "未提交") {
		t.Fatalf("dirty 应拒绝: code=%d\n%s\n%s", res.code, res.stdout, res.stderr)
	}
}

func TestE2ENoTask(t *testing.T) {
	r := setupRepo(t)
	res := runYacr(t, r.Dir, "", "validate")
	if res.code != 1 || !strings.Contains(res.stderr, "yacr task") {
		t.Fatalf("无任务时应提示先 task: code=%d\n%s\n%s", res.code, res.stdout, res.stderr)
	}
}

func TestE2EExplicitRangeAndFileUnit(t *testing.T) {
	r := fixture.Init(t)
	r.Write(".gitignore", ".yacr/\n")
	r.Write("data.bin", "\x00\x01old")
	base := r.Commit("base")
	r.Run("checkout", "-q", "-b", "feature")
	r.Write("data.bin", "\x00\x02new")
	r.Commit("binary change")

	res := runYacr(t, r.Dir, "", "task", "--range", base+"..HEAD")
	if res.code != 0 {
		t.Fatalf("task range: %s\n%s", res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, "文件级条目: 1") {
		t.Fatalf("binary 应为文件级条目: %s", res.stdout)
	}

	res = runYacr(t, r.Dir, upsertJSON("bin", "二进制更新", "数据文件重打包",
		[]map[string]any{{"file": "data.bin"}}, nil,
	), "report", "upsert", "--file", "-")
	if res.code != 0 {
		t.Fatalf("file-level upsert: %s\n%s", res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, "全部覆盖") {
		t.Fatalf("file-level coverage: %s", res.stdout)
	}
	res = runYacr(t, r.Dir, "", "validate", "--json")
	if res.code != 0 {
		t.Fatalf("validate: %s\n%s", res.stdout, res.stderr)
	}
	var v struct {
		Complete bool `json:"complete"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &v); err != nil || !v.Complete {
		t.Fatalf("validate json: %s", res.stdout)
	}
}

func TestE2EVersion(t *testing.T) {
	res := runYacr(t, t.TempDir(), "", "version")
	if res.code != 0 || !strings.HasPrefix(res.stdout, "yacr ") {
		t.Fatalf("version: code=%d out=%q", res.code, res.stdout)
	}
}
