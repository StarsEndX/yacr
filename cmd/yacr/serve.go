package main

import (
	"fmt"
	"os"

	"yacr/internal/app"
	"yacr/internal/mcpserver"
)

func cmdServe(args []string) (error, int) {
	var g globalFlags
	_ = parseGlobal(&g, args)
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
		return fmt.Errorf("MCP server 需要在 git 仓库内运行: %w", err), 1
	}
	if err := mcpserver.New(repoDir, g.target).Serve(os.Stdin, os.Stdout); err != nil {
		return fmt.Errorf("MCP server 退出: %w", err), 1
	}
	return nil, 0
}
