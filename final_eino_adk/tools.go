package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	adkfs "github.com/cloudwego/eino/adk/filesystem"
	adkfsmw "github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/plantask"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

var workdir = findWorkspaceRoot(mustGetwd())

func mustGetwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func findWorkspaceRoot(cwd string) string {
	for dir := filepath.Clean(cwd); ; dir = filepath.Dir(dir) {
		if isRepoWorkspaceRoot(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Clean(cwd)
		}
	}
}

func isRepoWorkspaceRoot(dir string) bool {
	required := []string{"go.mod", "final_eino_adk", "s01_agent_loop", "s19_mcp_plugin"}
	for _, name := range required {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
}

// safePath keeps all filesystem middleware access under the current workspace.
func safePath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		p = "."
	}
	joined := p
	if !filepath.IsAbs(joined) {
		joined = filepath.Join(workdir, joined)
	}
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	if isInsideWorkspace(abs) {
		return abs, nil
	}
	if mapped, ok := remapWorkspaceAlias(abs); ok {
		return mapped, nil
	}
	return "", fmt.Errorf("path escapes workspace: %s", p)
}

func isInsideWorkspace(abs string) bool {
	rel, err := filepath.Rel(workdir, abs)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func remapWorkspaceAlias(abs string) (string, bool) {
	workspaceName := filepath.Base(workdir)
	parts := strings.Split(strings.Trim(abs, string(filepath.Separator)), string(filepath.Separator))
	for i, part := range parts {
		if part != workspaceName {
			continue
		}
		candidate := workdir
		if i+1 < len(parts) {
			next := append([]string{workdir}, parts[i+1:]...)
			candidate = filepath.Join(next...)
		}
		candidateAbs, err := filepath.Abs(candidate)
		if err != nil || !isInsideWorkspace(candidateAbs) {
			continue
		}
		return candidateAbs, true
	}
	return "", false
}

type workspaceBackend struct {
	adkfs.Backend
	shell adkfs.Shell
}

func (b *workspaceBackend) LsInfo(ctx context.Context, req *adkfs.LsInfoRequest) ([]adkfs.FileInfo, error) {
	p, err := safePath(req.Path)
	if err != nil {
		return nil, err
	}
	files, err := b.Backend.LsInfo(ctx, &adkfs.LsInfoRequest{Path: p})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (b *workspaceBackend) Read(ctx context.Context, req *adkfs.ReadRequest) (*adkfs.FileContent, error) {
	p, err := safePath(req.FilePath)
	if err != nil {
		return nil, err
	}
	return b.Backend.Read(ctx, &adkfs.ReadRequest{
		FilePath: p,
		Offset:   req.Offset,
		Limit:    req.Limit,
	})
}

func (b *workspaceBackend) GrepRaw(ctx context.Context, req *adkfs.GrepRequest) ([]adkfs.GrepMatch, error) {
	path := req.Path
	if path == "" {
		path = "."
	}
	p, err := safePath(path)
	if err != nil {
		return nil, err
	}
	next := *req
	next.Path = p
	matches, err := b.Backend.GrepRaw(ctx, &next)
	if err != nil {
		return nil, err
	}
	return matches, nil
}

func (b *workspaceBackend) GlobInfo(ctx context.Context, req *adkfs.GlobInfoRequest) ([]adkfs.FileInfo, error) {
	path := req.Path
	if path == "" {
		path = "."
	}
	p, err := safePath(path)
	if err != nil {
		return nil, err
	}
	files, err := b.Backend.GlobInfo(ctx, &adkfs.GlobInfoRequest{
		Pattern: req.Pattern,
		Path:    p,
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (b *workspaceBackend) Write(ctx context.Context, req *adkfs.WriteRequest) error {
	p, err := safePath(req.FilePath)
	if err != nil {
		return err
	}
	return b.Backend.Write(ctx, &adkfs.WriteRequest{
		FilePath: p,
		Content:  req.Content,
	})
}

func (b *workspaceBackend) Edit(ctx context.Context, req *adkfs.EditRequest) error {
	p, err := safePath(req.FilePath)
	if err != nil {
		return err
	}
	return b.Backend.Edit(ctx, &adkfs.EditRequest{
		FilePath:   p,
		OldString:  req.OldString,
		NewString:  req.NewString,
		ReplaceAll: req.ReplaceAll,
	})
}

func (b *workspaceBackend) MultiModalRead(ctx context.Context, req *adkfs.MultiModalReadRequest) (*adkfs.MultiFileContent, error) {
	reader, ok := b.Backend.(adkfs.MultiModalReader)
	if !ok {
		content, err := b.Read(ctx, &req.ReadRequest)
		if err != nil {
			return nil, err
		}
		return &adkfs.MultiFileContent{FileContent: content}, nil
	}
	p, err := safePath(req.FilePath)
	if err != nil {
		return nil, err
	}
	next := *req
	next.FilePath = p
	return reader.MultiModalRead(ctx, &next)
}

func (b *workspaceBackend) Execute(ctx context.Context, input *adkfs.ExecuteRequest) (*adkfs.ExecuteResponse, error) {
	if b.shell == nil {
		return nil, errors.New("shell is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	resp, err := b.shell.Execute(ctx, input)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &adkfs.ExecuteResponse{Output: "Error: Timeout (120s)"}, nil
	}
	return resp, err
}

type taskBackend struct {
	backend *workspaceBackend
}

func (b *taskBackend) LsInfo(ctx context.Context, req *plantask.LsInfoRequest) ([]plantask.FileInfo, error) {
	files, err := b.backend.LsInfo(ctx, (*adkfs.LsInfoRequest)(req))
	if err != nil {
		return nil, err
	}
	for i := range files {
		if !filepath.IsAbs(files[i].Path) && filepath.Dir(files[i].Path) == "." {
			files[i].Path = filepath.Join(req.Path, files[i].Path)
		}
	}
	return files, nil
}

func (b *taskBackend) Read(ctx context.Context, req *plantask.ReadRequest) (*adkfsmw.FileContent, error) {
	return b.backend.Read(ctx, (*adkfs.ReadRequest)(req))
}

func (b *taskBackend) Write(ctx context.Context, req *plantask.WriteRequest) error {
	return b.backend.Write(ctx, (*adkfs.WriteRequest)(req))
}

func (b *taskBackend) Delete(ctx context.Context, req *plantask.DeleteRequest) error {
	p, err := safePath(req.FilePath)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

var denyList = []string{"rm -rf /", "sudo", "shutdown", "reboot", "mkfs", "dd if=", "> /dev/sda"}

func checkDenyList(command string) string {
	for _, p := range denyList {
		if strings.Contains(command, p) {
			return fmt.Sprintf("blocked dangerous command containing %q", p)
		}
	}
	return ""
}

type permRule struct {
	tools   []string
	check   func(args map[string]any) bool
	message string
}

var permissionRules = []permRule{
	{
		tools: []string{"write_file", "edit_file"},
		check: func(args map[string]any) bool {
			path := firstString(args, "file_path", "path")
			if path == "" {
				return false
			}
			_, err := safePath(path)
			return err != nil
		},
		message: "Writing outside workspace",
	},
	{
		tools: []string{"execute", "bash", "background_execute"},
		check: func(args map[string]any) bool {
			cmd := firstString(args, "command")
			return strings.Contains(cmd, "rm ") ||
				strings.Contains(cmd, "> /etc/") ||
				strings.Contains(cmd, "chmod 777")
		},
		message: "Potentially destructive command",
	},
	{
		tools: []string{"remove_worktree"},
		check: func(args map[string]any) bool {
			force, _ := args["force"].(bool)
			return force
		},
		message: "Force-removing a worktree",
	},
}

func firstString(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, _ := args[key].(string); v != "" {
			return v
		}
	}
	return ""
}

func checkRules(toolName string, args map[string]any) string {
	for _, r := range permissionRules {
		if slices.Contains(r.tools, toolName) && r.check(args) {
			return r.message
		}
	}
	return ""
}

type permissionMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
}

func newPermissionMiddleware() adk.ChatModelAgentMiddleware {
	return &permissionMiddleware{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}}
}

func (m *permissionMiddleware) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
		if allowed, reason := checkPermission(tCtx.Name, argumentsInJSON); !allowed {
			return "Permission denied: " + reason, nil
		}
		return endpoint(ctx, argumentsInJSON, opts...)
	}, nil
}

func checkPermission(toolName, rawArgs string) (bool, string) {
	var args map[string]any
	_ = json.Unmarshal([]byte(rawArgs), &args)

	if toolName == "execute" || toolName == "bash" || toolName == "background_execute" {
		cmd := firstString(args, "command")
		if reason := checkDenyList(cmd); reason != "" {
			fmt.Printf("\n\033[31m%s\033[0m\n", reason)
			return false, reason
		}
	}

	if reason := checkRules(toolName, args); reason != "" {
		if askUser(toolName, rawArgs, reason) == "deny" {
			return false, reason
		}
	}
	return true, ""
}

func askUser(toolName, rawArgs, reason string) string {
	fmt.Printf("\n\033[33m%s\033[0m\n", reason)
	fmt.Printf("Tool: %s(%s)\n", toolName, rawArgs)
	line, ok := readLine("Allow? [y/N] ")
	if !ok {
		return "deny"
	}
	switch strings.ToLower(line) {
	case "y", "yes":
		return "allow"
	default:
		return "deny"
	}
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "\n... (truncated)"
}

func extractJSONArray(text string) string {
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start < 0 || end < 0 || end < start {
		return ""
	}
	return text[start : end+1]
}

func textFromMessages(messages []*schema.Message, maxChars int) string {
	var parts []string
	for _, msg := range messages {
		if msg.Content == "" {
			continue
		}
		parts = append(parts, string(msg.Role)+": "+msg.Content)
	}
	return truncate(strings.Join(parts, "\n"), maxChars)
}
