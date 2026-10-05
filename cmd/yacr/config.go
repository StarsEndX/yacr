package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"yacr/internal/app"
	"yacr/internal/config"
)

func cmdConfig(args []string) (error, int) {
	if len(args) == 0 {
		return fmt.Errorf("用法: yacr config <base <ref>|base --unset|get>"), 1
	}
	var g globalFlags
	rest := parseGlobal(&g, args)
	sub := ""
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		sub = rest[0]
		rest = rest[1:]
	}
	dir := g.repo
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err, 1
		}
		dir = cwd
	}
	repoDir, err := app.FindRepoDir(dir)
	if err != nil {
		return err, 1
	}
	yacrDir := filepath.Join(repoDir, ".yacr")
	cfg := config.Load(yacrDir)

	switch sub {
	case "base":
		fs := flag.NewFlagSet("config base", flag.ContinueOnError)
		fs.Usage = func() {}
		unset := fs.Bool("unset", false, "清除记忆的 base")
		if err := fs.Parse(rest); err != nil {
			return fmt.Errorf("参数错误: %w", err), 1
		}
		if *unset {
			cfg.BaseRef = ""
			if err := config.Save(yacrDir, cfg); err != nil {
				return err, 1
			}
			fmt.Println("已清除记忆的 base")
			return nil, 0
		}
		if fs.NArg() != 1 {
			return fmt.Errorf("用法: yacr config base <ref> | yacr config base --unset"), 1
		}
		ref := fs.Arg(0)
		if !app.NewGit(repoDir).HasRef(ref) {
			return fmt.Errorf("引用 %s 在仓库中不存在（本地或远端均可）", ref), 1
		}
		cfg.BaseRef = ref
		if err := config.Save(yacrDir, cfg); err != nil {
			return err, 1
		}
		if g.json {
			_ = printJSON(map[string]any{"base_ref": ref})
		} else {
			fmt.Printf("已记忆 base: %s（之后 yacr task 将默认使用；--base 可临时覆盖）\n", ref)
		}
		return nil, 0
	case "get":
		if g.json {
			_ = printJSON(cfg)
		} else if cfg.BaseRef != "" {
			fmt.Printf("base: %s\n", cfg.BaseRef)
		} else {
			fmt.Println("（未配置 base）")
		}
		return nil, 0
	default:
		return fmt.Errorf("未知子命令 config %q", sub), 1
	}
}
