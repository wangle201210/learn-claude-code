package permission

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/textutil"
)

func (c Config) firstMatching(behavior, toolName string, args map[string]any) (Rule, bool) {
	var rules []Rule
	switch behavior {
	case "allow":
		rules = c.Allow
	case "ask":
		rules = c.Ask
	case "deny":
		rules = c.Deny
	default:
		return Rule{}, false
	}
	for _, rule := range rules {
		if ruleMatches(rule, toolName, args) {
			return rule, true
		}
	}
	return Rule{}, false
}

func (c Config) allowsByDefaultMode(toolName string) bool {
	mode := strings.ToLower(strings.TrimSpace(c.DefaultMode))
	if mode == "dontask" || mode == "bypasspermissions" {
		return true
	}
	if mode == "acceptedits" {
		return isFileEditTool(toolName)
	}
	return false
}

func (c Config) deniesByDefaultMode(toolName string) bool {
	mode := strings.ToLower(strings.TrimSpace(c.DefaultMode))
	return mode == "plan" && isWriteLikeTool(toolName)
}

func ruleMatches(rule Rule, toolName string, args map[string]any) bool {
	names := toolNameAliases(toolName)
	if !slices.Contains(names, rule.Tool) && !matchesMCPServerRule(rule.Tool, names) {
		return false
	}
	if rule.Content == "" {
		return true
	}
	return ruleContentMatches(rule, toolName, args)
}

func ruleContentMatches(rule Rule, toolName string, args map[string]any) bool {
	content := strings.TrimSpace(rule.Content)
	if content == "" {
		return true
	}
	if strings.HasSuffix(content, ":*") {
		prefix := strings.TrimSuffix(content, ":*")
		command := commandForPermission(toolName, args)
		return command == prefix || strings.HasPrefix(command, prefix+" ")
	}
	if strings.Contains(content, "*") {
		command := commandForPermission(toolName, args)
		if command != "" && wildcardMatch(content, command) {
			return true
		}
		path := pathForPermission(args)
		if path != "" && wildcardMatch(content, path) {
			return true
		}
		return false
	}
	command := commandForPermission(toolName, args)
	if command != "" {
		return strings.HasPrefix(command, content) || strings.Contains(command, content)
	}
	path := pathForPermission(args)
	if path != "" {
		return path == content || strings.Contains(path, content) || wildcardMatch(content, path)
	}
	request := textutil.FirstString(args, "request", "prompt", "name")
	return request != "" && strings.Contains(request, content)
}

func commandForPermission(toolName string, args map[string]any) string {
	if isShellTool(toolName) {
		return textutil.FirstString(args, "command")
	}
	return ""
}

func pathForPermission(args map[string]any) string {
	return textutil.FirstString(args, "file_path", "path")
}

func isShellTool(toolName string) bool {
	return slices.Contains(toolNameAliases(toolName), "Bash")
}

func isFileEditTool(toolName string) bool {
	names := toolNameAliases(toolName)
	return slices.Contains(names, "Edit") || slices.Contains(names, "Write") || slices.Contains(names, "MultiEdit")
}

func isWriteLikeTool(toolName string) bool {
	if isFileEditTool(toolName) {
		return true
	}
	switch normalizeRuleTool(toolName) {
	case "Bash", "background_execute", "create_worktree", "remove_worktree", "keep_worktree", "write_todos", "task", "teammate", "spawn_teammate", "compact":
		return true
	default:
		return false
	}
}

func normalizeRuleTool(tool string) string {
	tool = strings.TrimSpace(tool)
	switch strings.ToLower(tool) {
	case "bash", "execute":
		return "Bash"
	case "read", "read_file":
		return "Read"
	case "write", "write_file":
		return "Write"
	case "edit", "edit_file":
		return "Edit"
	case "multiedit", "multi_edit":
		return "MultiEdit"
	case "agent", "task":
		return "task"
	default:
		return tool
	}
}

func toolNameAliases(toolName string) []string {
	toolName = strings.TrimSpace(toolName)
	canonical := normalizeRuleTool(toolName)
	aliases := []string{canonical}
	switch canonical {
	case "Bash":
		aliases = append(aliases, "execute", "bash", "background_execute")
	case "Read":
		aliases = append(aliases, "read_file")
	case "Write":
		aliases = append(aliases, "write_file")
	case "Edit":
		aliases = append(aliases, "edit_file")
	case "task":
		aliases = append(aliases, "Agent")
	}
	if toolName != "" && !slices.Contains(aliases, toolName) {
		aliases = append(aliases, toolName)
	}
	return aliases
}

func matchesMCPServerRule(ruleTool string, aliases []string) bool {
	if !strings.HasPrefix(ruleTool, "mcp__") {
		return false
	}
	rulePrefix := strings.TrimSuffix(ruleTool, "__*")
	for _, alias := range aliases {
		if alias == ruleTool {
			return true
		}
		if strings.HasSuffix(ruleTool, "__*") && strings.HasPrefix(alias, rulePrefix+"__") {
			return true
		}
		if strings.Count(ruleTool, "__") == 1 && strings.HasPrefix(alias, ruleTool+"__") {
			return true
		}
	}
	return false
}

func wildcardMatch(pattern, value string) bool {
	if pattern == "" {
		return value == ""
	}
	ok, err := filepath.Match(pattern, value)
	if err == nil && ok {
		return true
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == value
	}
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		idx := strings.Index(value[pos:], part)
		if idx < 0 {
			return false
		}
		if i == 0 && !strings.HasPrefix(value, part) {
			return false
		}
		pos += idx + len(part)
	}
	last := parts[len(parts)-1]
	return last == "" || strings.HasSuffix(value, last)
}
