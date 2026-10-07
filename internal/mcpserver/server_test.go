package mcpserver_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"yacr/internal/fixture"
	"yacr/internal/mcpserver"
	"yacr/internal/taskgen"
)

func setupRepo(t *testing.T) string {
	t.Helper()
	r := fixture.Init(t)
	r.Write(".gitignore", ".yacr/\n")
	r.Write("a.txt", "one\ntwo\nthree\n")
	base := r.Commit("base")
	r.Write("a.txt", "one\nTWO\nthree\nFOUR\n")
	r.Commit("change")
	if _, err := taskgen.Generate(r.Git, filepath.Join(r.Dir, ".yacr"), taskgen.Options{ExplicitBase: base}); err != nil {
		t.Fatal(err)
	}
	return r.Dir
}

type resp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func call(t *testing.T, repoDir string, requests []string) []resp {
	t.Helper()
	var in bytes.Buffer
	for _, r := range requests {
		in.WriteString(r)
		in.WriteString("\n")
	}
	var out bytes.Buffer
	if err := mcpserver.New(repoDir, "").Serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	var resps []resp
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var r resp
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("响应解析失败: %v\n%s", err, line)
		}
		resps = append(resps, r)
	}
	return resps
}

func toolCallReq(id int, name string, args map[string]any) string {
	b, _ := json.Marshal(args)
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, id, name, string(b))
}

func rawMsg(r resp) json.RawMessage { return r.Result }

func TestMCPFlow(t *testing.T) {
	repoDir := setupRepo(t)

	resps := call(t, repoDir, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	})
	if len(resps) != 2 {
		t.Fatalf("responses: %d（notification 不应有响应）", len(resps))
	}
	if resps[0].Error != nil || !strings.Contains(string(resps[0].Result), "yacr") {
		t.Fatalf("initialize: %+v", resps[0])
	}
	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(rawMsg(resps[1]), &list); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range list.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"get_task", "get_files", "get_diff", "query_location", "report_upsert", "report_delete", "report_summary", "validate"} {
		if !names[want] {
			t.Fatalf("缺少 tool %s: %v", want, names)
		}
	}

	resps = call(t, repoDir, []string{
		toolCallReq(1, "query_location", map[string]any{"file": "a.txt", "line": 2}),
		toolCallReq(2, "report_upsert", map[string]any{
			"slug": "two", "title": "改 two", "explanation": "把 two 大写",
			"locations": []map[string]any{
				{"file": "a.txt", "side": "new", "start": 2, "end": 2},
				{"file": "a.txt", "side": "old", "start": 2, "end": 2},
			},
		}),
		toolCallReq(3, "report_upsert", map[string]any{
			"title": "bad", "explanation": "x",
			"locations": []map[string]any{{"file": "a.txt", "side": "new", "start": 1, "end": 1}},
		}),
		toolCallReq(4, "report_upsert", map[string]any{
			"slug": "four", "title": "加 FOUR", "explanation": "追加",
			"locations": []map[string]any{{"file": "a.txt", "side": "new", "start": 4, "end": 4}},
		}),
		toolCallReq(5, "validate", map[string]any{}),
	})
	if len(resps) != 5 {
		t.Fatalf("responses: %d", len(resps))
	}

	var q struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	json.Unmarshal(rawMsg(resps[0]), &q)
	if q.IsError || !strings.Contains(q.Content[0].Text, `"covered":false`) {
		t.Fatalf("query_location(2) 应未覆盖: %s", q.Content[0].Text)
	}

	json.Unmarshal(rawMsg(resps[1]), &q)
	if q.IsError || !strings.Contains(q.Content[0].Text, "e-001") {
		t.Fatalf("upsert1: %s", q.Content[0].Text)
	}

	json.Unmarshal(rawMsg(resps[2]), &q)
	if !q.IsError || !strings.Contains(q.Content[0].Text, "location_no_changed_lines") {
		t.Fatalf("upsert2 应校验失败: isError=%v %s", q.IsError, q.Content[0].Text)
	}

	json.Unmarshal(rawMsg(resps[4]), &q)
	if q.IsError || !strings.Contains(q.Content[0].Text, `"complete":true`) {
		t.Fatalf("validate 应完整: %s", q.Content[0].Text)
	}
}

func TestMCPNoTask(t *testing.T) {
	r := fixture.Init(t)
	r.Write("a.txt", "x\n")
	r.Commit("init")
	resps := call(t, r.Dir, []string{toolCallReq(1, "get_task", map[string]any{})})
	var q struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	json.Unmarshal(rawMsg(resps[0]), &q)
	if !q.IsError || !strings.Contains(q.Content[0].Text, "no_task") {
		t.Fatalf("no_task: %s", q.Content[0].Text)
	}
}

func TestMCPBadMethod(t *testing.T) {
	repoDir := setupRepo(t)
	resps := call(t, repoDir, []string{`{"jsonrpc":"2.0","id":9,"method":"resources/list"}`})
	if resps[0].Error == nil || resps[0].Error.Code != -32601 {
		t.Fatalf("unknown method: %+v", resps[0])
	}
}

func TestMCPPVersionNegotiation(t *testing.T) {
	repoDir := setupRepo(t)
	resps := call(t, repoDir, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
	})
	var r struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	json.Unmarshal(rawMsg(resps[0]), &r)
	if r.ProtocolVersion != "2024-11-05" {
		t.Fatalf("应回显已知版本: %s", r.ProtocolVersion)
	}
	json.Unmarshal(rawMsg(resps[1]), &r)
	if r.ProtocolVersion != "2025-06-18" {
		t.Fatalf("未知版本应回退到最新: %s", r.ProtocolVersion)
	}
}

func TestMCPEmptyArguments(t *testing.T) {
	repoDir := setupRepo(t)
	resps := call(t, repoDir, []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report_upsert"}}`,
	})
	var q struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	json.Unmarshal(rawMsg(resps[0]), &q)
	if !q.IsError || !strings.Contains(q.Content[0].Text, "invalid_entry") {
		t.Fatalf("空 arguments 应得到结构化校验错误而非解析错误: %s", q.Content[0].Text)
	}
}

func TestMCPToolSchemas(t *testing.T) {
	repoDir := setupRepo(t)
	resps := call(t, repoDir, []string{`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`})
	var list struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Type       string         `json:"type"`
				Properties map[string]any `json:"properties"`
				Required   any            `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(rawMsg(resps[0]), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) == 0 {
		t.Fatal("tools/list 为空")
	}
	for _, tl := range list.Tools {
		if tl.InputSchema.Type != "object" {
			t.Errorf("%s: inputSchema.type = %q，应为 object", tl.Name, tl.InputSchema.Type)
		}
		if tl.InputSchema.Properties == nil {
			t.Errorf("%s: inputSchema.properties 为 null，严格 MCP 客户端会拒绝该工具", tl.Name)
		}
		if tl.InputSchema.Required != nil {
			if _, ok := tl.InputSchema.Required.([]any); !ok {
				t.Errorf("%s: inputSchema.required 非数组: %T", tl.Name, tl.InputSchema.Required)
			}
		}
	}
}

func TestMCPReadToolsNotNull(t *testing.T) {
	repoDir := setupRepo(t)
	resps := call(t, repoDir, []string{
		toolCallReq(1, "get_task", nil),
		toolCallReq(2, "get_files", nil),
		toolCallReq(3, "get_commits", nil),
		toolCallReq(4, "get_diff", nil),
		toolCallReq(5, "get_diff", map[string]any{"file": "a.txt"}),
		toolCallReq(6, "report_upsert", map[string]any{
			"slug": "s", "title": "t", "explanation": "e",
			"locations": []map[string]any{
				{"file": "a.txt", "side": "new", "start": 2, "end": 2},
				{"file": "a.txt", "side": "old", "start": 2, "end": 2},
				{"file": "a.txt", "side": "new", "start": 4, "end": 4},
			},
		}),
		toolCallReq(7, "report_summary", map[string]any{"text": "总评"}),
		toolCallReq(8, "report_delete", map[string]any{"id_or_slug": "s"}),
	})
	for i, r := range resps {
		var q struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(rawMsg(r), &q); err != nil {
			t.Fatalf("tool %d 响应解析失败: %v", i+1, err)
		}
		if q.IsError {
			t.Errorf("tool %d 返回错误: %s", i+1, q.Content[0].Text)
		}
		if len(q.Content) == 0 || q.Content[0].Text == "" || q.Content[0].Text == "null" {
			t.Errorf("tool %d 返回空/null 结果", i+1)
		}
	}
}

