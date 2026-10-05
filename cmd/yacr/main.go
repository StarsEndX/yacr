package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"yacr/internal/app"
	"yacr/internal/report"
)

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }

func exitf(code int, format string, args ...any) error {
	return &exitError{code: code, err: fmt.Errorf(format, args...)}
}

var version = "0.1.0"

func usage() string {
	return `yacr — 意图追踪式 AI code review 工具

用法:
  yacr task    [--base <ref> | --range <base>..<head>] [--repo <dir>] [--json]
               解析 review 范围并生成任务包
  yacr show    [<file>[:<line>[-<end>]][:<side>]] [--repo <dir>] [--json]
               查询变更总览 / 某文件 / 某行的变更事实与已有解释
  yacr report  list | upsert | delete | summary [--repo <dir>] [--json]
               报告的固定变更接口（每次变更即时校验）
  yacr validate [--repo <dir>] [--json]
               校验报告覆盖率与引用完整性
  yacr view    [--repo <dir>]
               人类只读 TUI
  yacr done    [--force] [--repo <dir>]
               标记当前 review 完成（记录 reviewed-head，支持后续增量 review）
  yacr feedback [-o <file>] [--repo <dir>]
               导出 markdown 摘要
  yacr serve   [--repo <dir>]
               MCP server（stdio，供 agent 会话使用）
  yacr version
               输出版本号

全局:
  --repo <dir>   目标仓库目录（默认当前目录）
  --json         机器可读输出（供 agent 消费）
`
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(usage())
		os.Exit(0)
	}
	cmd, rest := args[0], args[1:]

	var err error
	var code int
	switch cmd {
	case "task":
		err, code = cmdTask(rest)
	case "show":
		err, code = cmdShow(rest)
	case "report":
		err, code = cmdReport(rest)
	case "validate":
		err, code = cmdValidate(rest)
	case "view":
		err, code = cmdView(rest)
	case "done":
		err, code = cmdDone(rest)
	case "feedback":
		err, code = cmdFeedback(rest)
	case "serve":
		err, code = cmdServe(rest)
	case "version":
		fmt.Printf("yacr %s\n", version)
	case "help", "-h", "--help":
		fmt.Print(usage())
	default:
		err = fmt.Errorf("未知命令 %q\n\n%s", cmd, usage())
		code = 1
	}
	if err != nil {
		var ve *report.ValError
		if e, ok := err.(*exitError); ok {
			code = e.code
			err = e.err
		}
		if code == 0 {
			code = 1
		}
		msg := err.Error()
		if !strings.HasPrefix(msg, "yacr") {
			msg = "yacr: " + msg
		}
		if ok := asValError(err, &ve); ok {
			b, _ := json.Marshal(map[string]any{"error": ve})
			fmt.Fprintf(os.Stderr, "%s\n", b)
		} else {
			fmt.Fprintln(os.Stderr, msg)
		}
		os.Exit(code)
	}
}

func asValError(err error, target **report.ValError) bool {
	for err != nil {
		if ve, ok := err.(*report.ValError); ok {
			*target = ve
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

type globalFlags struct {
	repo string
	json bool
}

func parseGlobal(flags *globalFlags, args []string) []string {
	out := args[:0]
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--repo" && i+1 < len(args):
			flags.repo = args[i+1]
			i += 2
		case strings.HasPrefix(a, "--repo="):
			flags.repo = strings.TrimPrefix(a, "--repo=")
			i++
		case a == "--json":
			flags.json = true
			i++
		default:
			out = append(out, a)
			i++
		}
	}
	return out
}

func loadCtx(g globalFlags, target string) (*app.Ctx, error) {
	dir := g.repo
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		dir = cwd
	}
	repoDir, err := app.FindRepoDir(dir)
	if err != nil {
		return nil, err
	}
	return app.Load(repoDir, target)
}

func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}
