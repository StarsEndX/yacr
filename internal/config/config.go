package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	BaseRef string `json:"base_ref,omitempty"`
}

func Path(yacrDir string) string {
	return filepath.Join(yacrDir, "config.json")
}

func Load(yacrDir string) Config {
	b, err := os.ReadFile(Path(yacrDir))
	if err != nil {
		return Config{}
	}
	var c Config
	if json.Unmarshal(b, &c) != nil {
		return Config{}
	}
	return c
}

func Save(yacrDir string, c Config) error {
	if err := os.MkdirAll(yacrDir, 0o755); err != nil {
		return fmt.Errorf("创建 .yacr 目录失败: %w", err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path(yacrDir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	return os.Rename(tmp, Path(yacrDir))
}
