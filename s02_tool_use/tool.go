package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

// workdir 是 agent 的工作区根目录，所有文件工具都被限制在它之内。
var workdir, _ = os.Getwd()

// toolHandler 接收工具调用的原始 JSON 参数，返回给模型的文本结果。
type toolHandler func(ctx context.Context, args string) string

// tool 把一个工具的元信息（给模型看）和执行函数（harness 调用）绑在一起。
type tool struct {
	info    *schema.ToolInfo
	handler toolHandler
}

// tools 是本章的工具集：s01 只有 bash，s02 扩展到 5 个。
// 加一个工具 = 往这里加一项，循环本身完全不用动。
var tools = []tool{
	{
		info: &schema.ToolInfo{
			Name: "bash",
			Desc: "Run a shell command.",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"command": {Type: schema.String, Desc: "The shell command to run.", Required: true},
			}),
		},
		handler: runBash,
	},
	{
		info: &schema.ToolInfo{
			Name: "read_file",
			Desc: "Read file contents.",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"path":  {Type: schema.String, Desc: "File path relative to the workspace.", Required: true},
				"limit": {Type: schema.Integer, Desc: "Max number of lines to read."},
			}),
		},
		handler: runRead,
	},
	{
		info: &schema.ToolInfo{
			Name: "write_file",
			Desc: "Write content to a file.",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"path":    {Type: schema.String, Desc: "File path relative to the workspace.", Required: true},
				"content": {Type: schema.String, Desc: "The full content to write.", Required: true},
			}),
		},
		handler: runWrite,
	},
	{
		info: &schema.ToolInfo{
			Name: "edit_file",
			Desc: "Replace exact text in a file once.",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"path":     {Type: schema.String, Desc: "File path relative to the workspace.", Required: true},
				"old_text": {Type: schema.String, Desc: "The exact text to find.", Required: true},
				"new_text": {Type: schema.String, Desc: "The text to replace it with.", Required: true},
			}),
		},
		handler: runEdit,
	},
	{
		info: &schema.ToolInfo{
			Name: "glob",
			Desc: "Find files matching a glob pattern.",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"pattern": {Type: schema.String, Desc: "Glob pattern, e.g. '*.go' or 'sub/*.txt'.", Required: true},
			}),
		},
		handler: runGlob,
	},
}

// toolInfos 收集所有工具定义，绑定到模型。
func toolInfos() []*schema.ToolInfo {
	infos := make([]*schema.ToolInfo, len(tools))
	for i, t := range tools {
		infos[i] = t.info
	}
	return infos
}

// handlers 是按工具名分发的查表映射，替代 s01 中硬编码的 runBash 调用。
func handlers() map[string]toolHandler {
	m := make(map[string]toolHandler, len(tools))
	for _, t := range tools {
		m[t.info.Name] = t.handler
	}
	return m
}

// safePath 把路径解析为绝对路径，并确保它没有逃出工作区。
// 注意：绝对路径要原样保留再校验——不能与 workdir 拼接，否则像 "/tmp/x"
// 这样的工作区外路径会被 filepath.Join 当相对片段悄悄塞进 workdir/tmp/x。
func safePath(p string) (string, error) {
	joined := p
	if !filepath.IsAbs(p) {
		joined = filepath.Join(workdir, p)
	}
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(workdir, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace: %s", p)
	}
	return abs, nil
}

// ── 工具实现 ──────────────────────────────────────────────

func runBash(ctx context.Context, args string) string {
	var a struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal([]byte(args), &a)

	dangerous := []string{"rm -rf /", "sudo", "shutdown", "reboot", "> /dev/"}
	for _, d := range dangerous {
		if strings.Contains(a.Command, d) {
			return "Error: Dangerous command blocked"
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", a.Command)
	cmd.Dir = workdir
	out, _ := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "Error: Timeout (120s)"
	}

	s := strings.TrimSpace(string(out))
	if s == "" {
		return "(no output)"
	}
	return truncate(s, 50000)
}

func runRead(ctx context.Context, args string) string {
	var a struct {
		Path  string `json:"path"`
		Limit int    `json:"limit"`
	}
	_ = json.Unmarshal([]byte(args), &a)

	p, err := safePath(a.Path)
	if err != nil {
		return "Error: " + err.Error()
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "Error: " + err.Error()
	}

	lines := strings.Split(string(data), "\n")
	if a.Limit > 0 && a.Limit < len(lines) {
		omitted := len(lines) - a.Limit
		lines = append(lines[:a.Limit:a.Limit], fmt.Sprintf("... (%d more lines)", omitted))
	}
	return strings.Join(lines, "\n")
}

func runWrite(ctx context.Context, args string) string {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	_ = json.Unmarshal([]byte(args), &a)

	p, err := safePath(a.Path)
	if err != nil {
		return "Error: " + err.Error()
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "Error: " + err.Error()
	}
	if err := os.WriteFile(p, []byte(a.Content), 0o644); err != nil {
		return "Error: " + err.Error()
	}
	return fmt.Sprintf("Wrote %d bytes to %s", len(a.Content), a.Path)
}

func runEdit(ctx context.Context, args string) string {
	var a struct {
		Path    string `json:"path"`
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	_ = json.Unmarshal([]byte(args), &a)

	p, err := safePath(a.Path)
	if err != nil {
		return "Error: " + err.Error()
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "Error: " + err.Error()
	}
	text := string(data)
	if !strings.Contains(text, a.OldText) {
		return fmt.Sprintf("Error: text not found in %s", a.Path)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(text, a.OldText, a.NewText, 1)), 0o644); err != nil {
		return "Error: " + err.Error()
	}
	return "Edited " + a.Path
}

func runGlob(ctx context.Context, args string) string {
	var a struct {
		Pattern string `json:"pattern"`
	}
	_ = json.Unmarshal([]byte(args), &a)

	pat := a.Pattern
	if !filepath.IsAbs(pat) {
		pat = filepath.Join(workdir, pat)
	}
	matches, err := filepath.Glob(pat)
	if err != nil {
		return "Error: " + err.Error()
	}

	var results []string
	for _, m := range matches {
		rel, err := filepath.Rel(workdir, m)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		results = append(results, rel)
	}
	if len(results) == 0 {
		return "(no matches)"
	}
	return strings.Join(results, "\n")
}

// truncate 按 rune 截断，避免切坏多字节字符（如中文）。
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
