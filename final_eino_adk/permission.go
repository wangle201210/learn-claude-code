package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
)

var denyList = []string{"rm -rf /", "sudo", "shutdown", "reboot", "mkfs", "dd if=", "> /dev/sda"}

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

func checkDenyList(command string) string {
	for _, p := range denyList {
		if strings.Contains(command, p) {
			return fmt.Sprintf("blocked dangerous command containing %q", p)
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
