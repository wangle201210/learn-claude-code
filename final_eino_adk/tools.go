package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// workdir 是 agent 的工作区根目录；文件类工具被限制在其下。
var workdir, _ = os.Getwd()

// safePath：相对路径 → 拼 workdir；绝对路径 → 直接用；最后 Rel 校验未逃出 workdir。
// （沿用 s05 修复后的逻辑：绝对路径不和 workdir 拼接，避免 /tmp/x 被悄悄改写成 workdir/tmp/x）
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

// ── bash ────────────────────────────────────────────────────

type bashArgs struct {
	Command string `json:"command" jsonschema:"required,description=Shell command to run via bash -c"`
}

func newBashTool() tool.InvokableTool {
	t, _ := utils.InferTool("bash", "Run a shell command.",
		func(ctx context.Context, a *bashArgs) (string, error) {
			dangerous := []string{"rm -rf /", "sudo", "shutdown", "reboot", "mkfs", "dd if=", "> /dev/sda"}
			for _, d := range dangerous {
				if strings.Contains(a.Command, d) {
					return "", fmt.Errorf("blocked dangerous command containing %q", d)
				}
			}
			ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "-c", a.Command)
			cmd.Dir = workdir
			out, _ := cmd.CombinedOutput()
			if ctx.Err() == context.DeadlineExceeded {
				return "Error: Timeout (120s)", nil
			}
			s := strings.TrimSpace(string(out))
			if s == "" {
				return "(no output)", nil
			}
			if r := []rune(s); len(r) > 50000 {
				return string(r[:50000]), nil
			}
			return s, nil
		})
	return t
}

// ── read_file ───────────────────────────────────────────────

type readFileArgs struct {
	Path  string `json:"path" jsonschema:"required,description=File path relative to the workspace (or absolute within workspace)"`
	Limit int    `json:"limit,omitempty" jsonschema:"description=Optional max number of lines to read"`
}

func newReadTool() tool.InvokableTool {
	t, _ := utils.InferTool("read_file", "Read file contents.",
		func(ctx context.Context, a *readFileArgs) (string, error) {
			p, err := safePath(a.Path)
			if err != nil {
				return "", err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return "", err
			}
			lines := strings.Split(string(data), "\n")
			if a.Limit > 0 && a.Limit < len(lines) {
				omitted := len(lines) - a.Limit
				lines = append(lines[:a.Limit:a.Limit], fmt.Sprintf("... (%d more lines)", omitted))
			}
			return strings.Join(lines, "\n"), nil
		})
	return t
}

// ── write_file ──────────────────────────────────────────────

type writeFileArgs struct {
	Path    string `json:"path" jsonschema:"required,description=File path relative to the workspace"`
	Content string `json:"content" jsonschema:"required,description=The full content to write"`
}

func newWriteTool() tool.InvokableTool {
	t, _ := utils.InferTool("write_file", "Write content to a file (creates parents).",
		func(ctx context.Context, a *writeFileArgs) (string, error) {
			p, err := safePath(a.Path)
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(p, []byte(a.Content), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("Wrote %d bytes to %s", len(a.Content), a.Path), nil
		})
	return t
}

// ── glob ────────────────────────────────────────────────────

type globArgs struct {
	Pattern string `json:"pattern" jsonschema:"required,description=Glob pattern e.g. '*.go' or 'sub/*.txt'"`
}

func newGlobTool() tool.InvokableTool {
	t, _ := utils.InferTool("glob", "Find files matching a glob pattern.",
		func(ctx context.Context, a *globArgs) (string, error) {
			pat := a.Pattern
			if !filepath.IsAbs(pat) {
				pat = filepath.Join(workdir, pat)
			}
			matches, err := filepath.Glob(pat)
			if err != nil {
				return "", err
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
				return "(no matches)", nil
			}
			return strings.Join(results, "\n"), nil
		})
	return t
}
