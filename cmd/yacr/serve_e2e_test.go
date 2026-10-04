package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func execCommand(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }

func envNoColor() []string {
	return append(os.Environ(), "TERM=dumb", "NO_COLOR=1")
}

func TestE2EServeMCP(t *testing.T) {
	r := setupRepo(t)
	if res := runYacr(t, r.Dir, "", "task"); res.code != 0 {
		t.Fatalf("task: %s", res.stderr)
	}

	stdin := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"query_location","arguments":{"file":"app.go","line":9}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"report_upsert","arguments":{"slug":"auth","title":"登录校验","explanation":"增加 login 与 extra","locations":[{"file":"app.go","side":"new","start":3,"end":9},{"file":"app.go","side":"old","start":3,"end":3},{"file":"auth.go","side":"new","start":1,"end":3}]}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"validate","arguments":{}}}`,
	}, "\n") + "\n"

	cmd := execCommand(binaryPath, "serve", "--repo", r.Dir)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Env = envNoColor()
	if err := cmd.Run(); err != nil {
		t.Fatalf("serve: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("应答数: %d\n%s", len(lines), out.String())
	}
	var last struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[3]), &last); err != nil {
		t.Fatal(err)
	}
	if last.Result.IsError || !strings.Contains(last.Result.Content[0].Text, `"complete":true`) {
		t.Fatalf("validate via MCP: %s", last.Result.Content[0].Text)
	}

	res := runYacr(t, r.Dir, "", "validate")
	if res.code != 0 {
		t.Fatalf("CLI 侧应看到完整覆盖: %s\n%s", res.stdout, res.stderr)
	}
}
