package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// stdin 由 REPL 和工具审批共用，避免两个 reader 各自缓冲导致丢数据。
var stdin = bufio.NewReader(os.Stdin)

func readLine(prompt string) (string, bool) {
	fmt.Print(prompt)
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSpace(line), true
}

// ═══════════════════════════════════════════════════════════
//  Hook 系统：把扩展逻辑从 loop 里搬到注册表上，loop 保持干净。
// ═══════════════════════════════════════════════════════════

type hookEvent string

const (
	eventUserPromptSubmit hookEvent = "UserPromptSubmit"
	eventPreToolUse       hookEvent = "PreToolUse"
	eventPostToolUse      hookEvent = "PostToolUse"
	eventStop             hookEvent = "Stop"
)

// hookCtx 携带各事件需要的数据；不同事件只用其中相关字段。
type hookCtx struct {
	query    string            // UserPromptSubmit
	toolName string            // Pre/PostToolUse
	rawArgs  string            // Pre/PostToolUse：工具调用的原始 JSON 参数
	output   string            // PostToolUse：工具执行结果
	messages []*schema.Message // Stop
}

// hook 返回非空字符串表示"拦截/注入"（对应 Python 的返回非 None）：
//   - PreToolUse：非空 = 阻止该工具调用，字符串作为给模型的理由
//   - Stop：非空 = 强制继续循环，字符串作为新的 user 消息
//   - 其它事件：返回 "" 即可（纯观测）
type hook func(c *hookCtx) string

var hooks = map[hookEvent][]hook{}

func registerHook(event hookEvent, h hook) {
	hooks[event] = append(hooks[event], h)
}

// triggerHooks 依次调用该事件的回调，返回第一个非空结果（短路）。
func triggerHooks(event hookEvent, c *hookCtx) string {
	for _, h := range hooks[event] {
		if r := h(c); r != "" {
			return r
		}
	}
	return ""
}

// ── 权限相关的常量（s03 的逻辑搬到 permissionHook 里）──────────
var denyList = []string{"rm -rf /", "sudo", "shutdown", "reboot", "mkfs", "dd if="}
var destructive = []string{"rm ", "> /etc/", "chmod 777"}

func isYes(line string, ok bool) bool {
	if !ok {
		return false
	}
	s := strings.ToLower(line)
	return s == "y" || s == "yes"
}

// permissionHook（PreToolUse）：s03 的三闸门逻辑，现在以 hook 形式存在。
func permissionHook(c *hookCtx) string {
	var args map[string]any
	_ = json.Unmarshal([]byte(c.rawArgs), &args)

	if c.toolName == "bash" {
		cmd, _ := args["command"].(string)
		for _, p := range denyList {
			if strings.Contains(cmd, p) {
				fmt.Printf("\n\033[31m⛔ Blocked: '%s'\033[0m\n", p)
				return "Permission denied by deny list"
			}
		}
		for _, kw := range destructive {
			if strings.Contains(cmd, kw) {
				fmt.Print("\n\033[33m⚠  Potentially destructive command\033[0m\n")
				fmt.Printf("   Tool: %s(%s)\n", c.toolName, c.rawArgs)
				if !isYes(readLine("   Allow? [y/N] ")) {
					return "Permission denied by user"
				}
			}
		}
	}

	if c.toolName == "write_file" || c.toolName == "edit_file" {
		path, _ := args["path"].(string)
		if _, err := safePath(path); err != nil {
			fmt.Print("\n\033[33m⚠  Writing outside workspace\033[0m\n")
			fmt.Printf("   Tool: %s(%s)\n", c.toolName, c.rawArgs)
			if !isYes(readLine("   Allow? [y/N] ")) {
				return "Permission denied by user"
			}
		}
	}
	return ""
}

// logHook（PreToolUse）：记录每次工具调用。注册在 permissionHook 之后，
// 所以被拒的调用会因短路而不打印这行。
func logHook(c *hookCtx) string {
	fmt.Printf("\033[90m[HOOK] %s(%s)\033[0m\n", c.toolName, truncate(c.rawArgs, 60))
	return ""
}

// largeOutputHook（PostToolUse）：输出过大时警告。
func largeOutputHook(c *hookCtx) string {
	if len(c.output) > 100000 {
		fmt.Printf("\033[33m[HOOK] ⚠ Large output from %s: %d chars\033[0m\n", c.toolName, len(c.output))
	}
	return ""
}

// contextInjectHook（UserPromptSubmit）：用户输入到达 LLM 之前先记录上下文。
func contextInjectHook(c *hookCtx) string {
	fmt.Printf("\033[90m[HOOK] UserPromptSubmit: working in %s\033[0m\n", workdir)
	return ""
}

// summaryHook（Stop）：循环即将退出时统计本会话用了多少次工具。
func summaryHook(c *hookCtx) string {
	count := 0
	for _, m := range c.messages {
		if m.Role == schema.Tool {
			count++
		}
	}
	fmt.Printf("\033[90m[HOOK] Stop: session used %d tool calls\033[0m\n", count)
	return ""
}

// 注册所有 hook。PreToolUse 里 permission 必须先于 log（被拒则短路、不记录）。
func init() {
	registerHook(eventUserPromptSubmit, contextInjectHook)
	registerHook(eventPreToolUse, permissionHook)
	registerHook(eventPreToolUse, logHook)
	registerHook(eventPostToolUse, largeOutputHook)
	registerHook(eventStop, summaryHook)
}
