package permission

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/textutil"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/workspace"
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
			path := textutil.FirstString(args, "file_path", "path")
			if path == "" {
				return false
			}
			_, err := workspace.SafePath(path)
			return err != nil
		},
		message: "Writing outside workspace",
	},
	{
		tools: []string{"execute", "bash", "background_execute"},
		check: func(args map[string]any) bool {
			cmd := textutil.FirstString(args, "command")
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
	prompt PromptFunc
	config Config
}

type PromptFunc func(prompt string) (string, bool)

func New(prompt PromptFunc) adk.ChatModelAgentMiddleware {
	mw, err := NewFromRoot(workspace.Dir(), prompt)
	if err != nil {
		return NewWithConfig(prompt, Config{})
	}
	return mw
}

func NewFromRoot(root string, prompt PromptFunc) (adk.ChatModelAgentMiddleware, error) {
	config, err := LoadConfig(root)
	if err != nil {
		return nil, err
	}
	return NewWithConfig(prompt, config), nil
}

func NewWithConfig(prompt PromptFunc, config Config) adk.ChatModelAgentMiddleware {
	return &permissionMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		prompt:                       prompt,
		config:                       config,
	}
}

func (m *permissionMiddleware) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
		if allowed, reason := m.checkPermission(tCtx.Name, argumentsInJSON); !allowed {
			return "Permission denied: " + reason, nil
		}
		return endpoint(ctx, argumentsInJSON, opts...)
	}, nil
}

func CheckDenyList(command string) string {
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

func (m *permissionMiddleware) checkPermission(toolName, rawArgs string) (bool, string) {
	var args map[string]any
	_ = json.Unmarshal([]byte(rawArgs), &args)

	if toolName == "execute" || toolName == "bash" || toolName == "background_execute" {
		cmd := textutil.FirstString(args, "command")
		if reason := CheckDenyList(cmd); reason != "" {
			fmt.Printf("\n\033[31m%s\033[0m\n", reason)
			return false, reason
		}
	}

	if rule, ok := m.config.firstMatching("deny", toolName, args); ok {
		reason := "Denied by permission rule " + rule.Raw
		fmt.Printf("\n\033[31m%s\033[0m\n", reason)
		return false, reason
	}

	if _, ok := m.config.firstMatching("allow", toolName, args); ok {
		return true, ""
	}

	if rule, ok := m.config.firstMatching("ask", toolName, args); ok {
		reason := "Permission rule " + rule.Raw + " requires approval"
		if m.askUser(toolName, rawArgs, reason) == "deny" {
			return false, reason
		}
		return true, ""
	}

	if m.config.deniesByDefaultMode(toolName) {
		reason := "Denied by permissions.defaultMode=" + m.config.DefaultMode
		fmt.Printf("\n\033[31m%s\033[0m\n", reason)
		return false, reason
	}

	if reason := checkRules(toolName, args); reason != "" {
		if m.config.allowsByDefaultMode(toolName) {
			return true, ""
		}
		if m.askUser(toolName, rawArgs, reason) == "deny" {
			return false, reason
		}
	}
	return true, ""
}

func (m *permissionMiddleware) askUser(toolName, rawArgs, reason string) string {
	fmt.Printf("\n\033[33m%s\033[0m\n", reason)
	fmt.Printf("Tool: %s(%s)\n", toolName, rawArgs)
	if m.prompt == nil {
		return "deny"
	}
	line, ok := m.prompt("Allow? [y/N] ")
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
