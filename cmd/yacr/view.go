package main

import (
	"fmt"
	"yacr/internal/tui"
)

func cmdView(args []string) (error, int) {
	var g globalFlags
	_ = parseGlobal(&g, args)
	ctx, err := loadCtx(g, "")
	if err != nil {
		return err, 1
	}
	if err := tui.Run(ctx); err != nil {
		return fmt.Errorf("TUI 运行失败（view 需要在终端中运行）: %w", err), 1
	}
	return nil, 0
}
