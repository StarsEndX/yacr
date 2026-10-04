package mcpserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"yacr/internal/app"
	"yacr/internal/diffmodel"
	"yacr/internal/report"
)

const protocolVersion = "2025-06-18"

type Server struct {
	repoDir string
}

func New(repoDir string) *Server {
	return &Server{repoDir: repoDir}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (s *Server) Serve(r io.Reader, w io.Writer) error {
	reader := bufio.NewReader(r)
	writer := bufio.NewWriter(w)
	for {
		line, err := reader.ReadString('\n')
		if line == "" && err != nil {
			return nil
		}
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			resp := s.handleLine(trimmed)
			if resp != nil {
				b, merr := json.Marshal(resp)
				if merr == nil {
					writer.Write(b)
					writer.WriteByte('\n')
					writer.Flush()
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *Server) handleLine(line string) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal([]byte(line), &req); err != nil || req.JSONRPC != "2.0" {
		return &rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32700, Message: "解析错误: " + err.Error()},
		}
	}
	if len(req.ID) == 0 {
		return nil
	}
	result, rerr := s.dispatch(req.Method, req.Params)
	if rerr != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rerr}
	}
	b, _ := json.Marshal(result)
	return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: b}
}

func (s *Server) dispatch(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "yacr", "version": "0.1.0"},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": toolDefs()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
		}
		text, isErr, ierr := s.callTool(p.Name, p.Arguments)
		if ierr != nil {
			return nil, ierr
		}
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": isErr,
		}, nil
	case "notifications/initialized":
		return nil, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
	}
}

func toolDefs() []map[string]any {
	locSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file":  map[string]any{"type": "string", "description": "文件路径（新侧/旧侧路径必须与 side 匹配）"},
			"side":  map[string]any{"type": "string", "enum": []string{"new", "old"}, "description": "new=新侧行号, old=旧侧行号；无行级变更的文件省略 side"},
			"start": map[string]any{"type": "integer"},
			"end":   map[string]any{"type": "integer"},
		},
		"required": []string{"file"},
	}
	mk := func(name, desc string, props map[string]any, required []string) map[string]any {
		return map[string]any{
			"name":        name,
			"description": desc,
			"inputSchema": map[string]any{"type": "object", "properties": props, "required": required},
		}
	}
	return []map[string]any{
		mk("get_task", "获取当前 review 任务：范围、提交、统计、覆盖率", nil, nil),
		mk("get_files", "列出变更文件总览（含各文件未解释行）", nil, nil),
		mk("get_commits", "列出范围内的提交（hash/主题/文件）", nil, nil),
		mk("get_diff", "查看 diff：给 file 看该文件 hunks 详情（含每行覆盖状态与解释条目）；都不给看全部文件摘要",
			map[string]any{
				"file": map[string]any{"type": "string"},
			}, nil),
		mk("query_location", "查询某个位置的变更事实与已有解释（回答『这里为什么改』时先调用它）",
			map[string]any{
				"file": map[string]any{"type": "string"},
				"line": map[string]any{"type": "integer"},
				"side": map[string]any{"type": "string", "enum": []string{"new", "old"}},
			}, []string{"file", "line"}),
		mk("report_upsert", "写入/更新解释条目（固定接口，即时校验：行定位、commit、结构）",
			map[string]any{
				"id":          map[string]any{"type": "string"},
				"slug":        map[string]any{"type": "string", "description": "幂等更新键"},
				"title":       map[string]any{"type": "string"},
				"explanation": map[string]any{"type": "string", "description": "解释内容（默认中文）"},
				"locations":   map[string]any{"type": "array", "items": locSchema},
				"commits":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"tags":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, []string{"title", "explanation", "locations"}),
		mk("report_delete", "删除解释条目", map[string]any{
			"id_or_slug": map[string]any{"type": "string"},
		}, []string{"id_or_slug"}),
		mk("report_summary", "设置报告总评", map[string]any{
			"text": map[string]any{"type": "string"},
		}, []string{"text"}),
		mk("validate", "校验覆盖率与引用完整性，返回未解释清单", nil, nil),
	}
}

func toolResult(v any, err error, verr *report.ValError) (string, bool, *rpcError) {
	if err != nil {
		b, _ := json.Marshal(map[string]any{"error": map[string]any{"code": "internal", "message": err.Error()}})
		return string(b), true, nil
	}
	if verr != nil {
		b, _ := json.Marshal(map[string]any{"error": verr})
		return string(b), true, nil
	}
	b, _ := json.Marshal(v)
	return string(b), false, nil
}

func (s *Server) callTool(name string, args json.RawMessage) (string, bool, *rpcError) {
	ctx, err := app.Load(s.repoDir, "")
	if err != nil {
		b, _ := json.Marshal(map[string]any{"error": map[string]any{
			"code": "no_task", "message": err.Error(),
			"hint": "先运行 `yacr task` 生成任务包",
		}})
		return string(b), true, nil
	}

	switch name {
	case "get_task":
		cov, files := ctx.Overview()
		return toolResult(map[string]any{
			"target_id": ctx.TargetID(), "meta": ctx.Meta,
			"coverage": cov, "files": files,
		}, nil, nil)
	case "get_files":
		cov, files := ctx.Overview()
		return toolResult(map[string]any{"coverage": cov, "files": files}, nil, nil)
	case "get_commits":
		return toolResult(map[string]any{"commits": ctx.Meta.Commits}, nil, nil)
	case "get_diff":
		var p struct {
			File string `json:"file"`
		}
		if len(args) > 0 {
			_ = json.Unmarshal(args, &p)
		}
		if p.File == "" {
			cov, files := ctx.Overview()
			return toolResult(map[string]any{"coverage": cov, "files": files}, nil, nil)
		}
		f := ctx.Model.FileByPath(p.File, diffmodel.SideNew)
		if f == nil {
			f = ctx.Model.FileByPath(p.File, diffmodel.SideOld)
		}
		if f == nil {
			return toolResult(nil, fmt.Errorf("文件 %s 不在本次 diff 中", p.File), nil)
		}
		hunks := make([]*app.HunkView, 0, len(f.Hunks))
		for _, h := range f.Hunks {
			if hv, err := ctx.HunkView(h.ID); err == nil {
				hunks = append(hunks, hv)
			}
		}
		return toolResult(map[string]any{
			"id": f.ID, "path": f.Path(), "status": string(f.Status), "hunks": hunks,
		}, nil, nil)
	case "query_location":
		var p struct {
			File string `json:"file"`
			Line int    `json:"line"`
			Side string `json:"side"`
		}
		if err := json.Unmarshal(args, &p); err != nil || p.File == "" || p.Line <= 0 {
			return toolResult(nil, fmt.Errorf("参数: {file, line, side?}"), nil)
		}
		res, qerr := ctx.Query(app.LocationQuery{File: p.File, Line: p.Line, End: p.Line, Side: p.Side})
		return toolResult(res, qerr, nil)
	case "report_upsert":
		var in report.EntryInput
		if err := json.Unmarshal(args, &in); err != nil {
			return toolResult(nil, fmt.Errorf("EntryInput 解析失败: %v", err), nil)
		}
		res, verr, uerr := ctx.Service.Upsert(ctx.TargetID(), in)
		return toolResult(res, uerr, verr)
	case "report_delete":
		var p struct {
			IDOrSlug string `json:"id_or_slug"`
		}
		if err := json.Unmarshal(args, &p); err != nil || p.IDOrSlug == "" {
			return toolResult(nil, fmt.Errorf("参数: {id_or_slug}"), nil)
		}
		cov, verr, derr := ctx.Service.Delete(ctx.TargetID(), p.IDOrSlug)
		return toolResult(map[string]any{"deleted": true, "coverage": cov}, derr, verr)
	case "report_summary":
		var p struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return toolResult(nil, fmt.Errorf("参数: {text}"), nil)
		}
		if err := ctx.Service.SetSummary(ctx.TargetID(), p.Text); err != nil {
			return toolResult(nil, err, nil)
		}
		return toolResult(map[string]any{"summary": p.Text}, nil, nil)
	case "validate":
		cov, files := ctx.Overview()
		return toolResult(map[string]any{
			"complete": cov.Complete, "coverage": cov, "files": files,
		}, nil, nil)
	default:
		return "", false, &rpcError{Code: -32602, Message: "unknown tool: " + name}
	}
}
