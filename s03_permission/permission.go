package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

// stdin 由 REPL 和工具审批共用，避免两个 reader 各自缓冲导致丢数据。
var stdin = bufio.NewReader(os.Stdin)

// readLine 打印提示并读取一行；遇到 EOF 返回 ok=false。
func readLine(prompt string) (string, bool) {
	fmt.Print(prompt)
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSpace(line), true
}

// ── Gate 1: 硬拒绝列表 —— 永远禁止，命中即拒，不问用户 ──────────
var denyList = []string{"rm -rf /", "sudo", "shutdown", "reboot", "mkfs", "dd if=", "> /dev/sda"}

func checkDenyList(command string) string {
	for _, p := range denyList {
		if strings.Contains(command, p) {
			return fmt.Sprintf("Blocked: '%s' is on the deny list", p)
		}
	}
	return ""
}

// ── Gate 2: 规则匹配 —— 取决于上下文，命中后交给 Gate 3 问用户 ────
type permRule struct {
	tools   []string
	check   func(args map[string]any) bool
	message string
}

var permissionRules = []permRule{
	{
		tools: []string{"write_file", "edit_file"},
		check: func(args map[string]any) bool {
			path, _ := args["path"].(string)
			_, err := safePath(path)
			return err != nil // 写到工作区之外
		},
		message: "Writing outside workspace",
	},
	{
		tools: []string{"bash"},
		check: func(args map[string]any) bool {
			cmd, _ := args["command"].(string)
			for _, kw := range []string{"rm ", "> /etc/", "chmod 777"} {
				if strings.Contains(cmd, kw) {
					return true
				}
			}
			return false
		},
		message: "Potentially destructive command",
	},
}

func checkRules(toolName string, args map[string]any) string {
	for _, r := range permissionRules {
		if slices.Contains(r.tools, toolName) && r.check(args) {
			return r.message
		}
	}
	return ""
}

// ── Gate 3: 用户审批 —— 规则命中后暂停等待确认 ──────────────────
func askUser(toolName, rawArgs, reason string) string {
	fmt.Printf("\n\033[33m⚠  %s\033[0m\n", reason)
	fmt.Printf("   Tool: %s(%s)\n", toolName, rawArgs)
	line, ok := readLine("   Allow? [y/N] ")
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

// checkPermission 把三道闸门串起来：硬拒绝优先，软询问次之，都没命中就放行。
func checkPermission(toolName, rawArgs string) bool {
	var args map[string]any
	_ = json.Unmarshal([]byte(rawArgs), &args)

	if toolName == "bash" {
		cmd, _ := args["command"].(string)
		if reason := checkDenyList(cmd); reason != "" {
			fmt.Printf("\n\033[31m⛔ %s\033[0m\n", reason)
			return false
		}
	}

	if reason := checkRules(toolName, args); reason != "" {
		if askUser(toolName, rawArgs, reason) == "deny" {
			return false
		}
	}
	return true
}
