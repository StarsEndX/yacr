package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Record struct {
	TargetID string `json:"target_id"`
	Branch   string `json:"branch"`
	Base     string `json:"base"`
	Head     string `json:"head"`
	DoneAt   string `json:"done_at"`
	Forced   bool   `json:"forced,omitempty"`
}

func sessionsDir(yacrDir string) string {
	return filepath.Join(yacrDir, "sessions")
}

func MarkDone(yacrDir string, rec Record) error {
	dir := sessionsDir(yacrDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建 sessions 目录失败: %w", err)
	}
	name := sanitize(rec.TargetID) + ".json"
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, name+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '.', '-', '_':
			return r
		default:
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				return r
			}
			return '_'
		}
	}, s)
}

func List(yacrDir string) []Record {
	entries, err := os.ReadDir(sessionsDir(yacrDir))
	if err != nil {
		return nil
	}
	var recs []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(sessionsDir(yacrDir), e.Name()))
		if err != nil {
			continue
		}
		var rec Record
		if json.Unmarshal(b, &rec) == nil {
			recs = append(recs, rec)
		}
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].DoneAt < recs[j].DoneAt })
	return recs
}

func LastReviewedHead(yacrDir, branch string) (string, bool) {
	recs := List(yacrDir)
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Branch == branch && recs[i].Head != "" {
			return recs[i].Head, true
		}
	}
	return "", false
}

func Find(yacrDir, targetID string) *Record {
	for _, r := range List(yacrDir) {
		if r.TargetID == targetID {
			return &r
		}
	}
	return nil
}
