package permission

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigReadsClaudeSettingsAndLocal(t *testing.T) {
	root := t.TempDir()
	writePermissionFile(t, filepath.Join(root, ".claude", "settings.json"), `{
  "permissions": {
    "allow": ["Bash(git:*)"],
    "ask": ["Edit"],
    "deny": ["Bash(rm *)"],
    "defaultMode": "plan"
  }
}`)
	writePermissionFile(t, filepath.Join(root, ".claude", "settings.local.json"), `{
  "permissions": {
    "allow": ["Write"],
    "deny": ["mcp__private__*"],
    "defaultMode": "acceptEdits"
  }
}`)

	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultMode != "acceptEdits" {
		t.Fatalf("default mode = %q, want local override acceptEdits", cfg.DefaultMode)
	}
	if len(cfg.Allow) != 2 || len(cfg.Ask) != 1 || len(cfg.Deny) != 2 {
		t.Fatalf("config = %#v, want merged allow/ask/deny rules", cfg)
	}
	if !ruleMatches(cfg.Allow[0], "execute", map[string]any{"command": "git status"}) {
		t.Fatal("Bash(git:*) should match execute git status")
	}
	if !ruleMatches(cfg.Allow[0], "execute", map[string]any{"command": "git"}) {
		t.Fatal("Bash(git:*) should match bare git")
	}
	if ruleMatches(cfg.Allow[0], "execute", map[string]any{"command": "go test ./..."}) {
		t.Fatal("Bash(git:*) should not match go test")
	}
}

func TestCheckPermissionUsesDenyBeforeAllow(t *testing.T) {
	mw := newTestMiddleware(Config{
		Allow: []Rule{{Raw: "Bash", Tool: "Bash"}},
		Deny:  []Rule{{Raw: "Bash(rm *)", Tool: "Bash", Content: "rm *"}},
	})

	allowed, reason := mw.checkPermission("execute", `{"command":"rm file.txt"}`)
	if allowed {
		t.Fatalf("permission allowed, want denied (%s)", reason)
	}
	if !strings.Contains(reason, "Bash(rm *)") {
		t.Fatalf("reason = %q, want matching deny rule", reason)
	}
}

func TestCheckPermissionAllowRuleSkipsPrompt(t *testing.T) {
	promptCalls := 0
	mw := newTestMiddlewareWithPrompt(Config{
		Allow: []Rule{{Raw: "Bash(git:*)", Tool: "Bash", Content: "git:*"}},
	}, func(string) (string, bool) {
		promptCalls++
		return "n", true
	})

	allowed, reason := mw.checkPermission("execute", `{"command":"git status"}`)
	if !allowed {
		t.Fatalf("permission denied: %s", reason)
	}
	if promptCalls != 0 {
		t.Fatalf("prompt calls = %d, want 0", promptCalls)
	}
}

func TestCheckPermissionAskRulePrompts(t *testing.T) {
	promptCalls := 0
	mw := newTestMiddlewareWithPrompt(Config{
		Ask: []Rule{{Raw: "Bash(go test *)", Tool: "Bash", Content: "go test *"}},
	}, func(string) (string, bool) {
		promptCalls++
		return "y", true
	})

	allowed, reason := mw.checkPermission("execute", `{"command":"go test ./..."}`)
	if !allowed {
		t.Fatalf("permission denied: %s", reason)
	}
	if promptCalls != 1 {
		t.Fatalf("prompt calls = %d, want 1", promptCalls)
	}
}

func TestDefaultModePlanDeniesWriteLikeTools(t *testing.T) {
	mw := newTestMiddleware(Config{DefaultMode: "plan"})

	allowed, reason := mw.checkPermission("write_file", `{"file_path":"README.md"}`)
	if allowed {
		t.Fatalf("permission allowed, want plan mode denial (%s)", reason)
	}
	if !strings.Contains(reason, "defaultMode=plan") {
		t.Fatalf("reason = %q, want defaultMode=plan", reason)
	}
}

func TestDefaultModeAcceptEditsAllowsEditRule(t *testing.T) {
	promptCalls := 0
	mw := newTestMiddlewareWithPrompt(Config{DefaultMode: "acceptEdits"}, func(string) (string, bool) {
		promptCalls++
		return "n", true
	})

	allowed, reason := mw.checkPermission("edit_file", `{"file_path":"../outside.txt"}`)
	if !allowed {
		t.Fatalf("permission denied: %s", reason)
	}
	if promptCalls != 0 {
		t.Fatalf("prompt calls = %d, want 0", promptCalls)
	}
}

func TestMCPServerRuleMatchesServerTools(t *testing.T) {
	rule := Rule{Raw: "mcp__private__*", Tool: "mcp__private__*"}
	if !ruleMatches(rule, "mcp__private__read", nil) {
		t.Fatal("mcp server wildcard should match private read tool")
	}
	if ruleMatches(rule, "mcp__public__read", nil) {
		t.Fatal("mcp server wildcard should not match public read tool")
	}
}

func TestFileRuleWildcardMatchesPath(t *testing.T) {
	rule := Rule{Raw: "Edit(*.go)", Tool: "Edit", Content: "*.go"}
	if !ruleMatches(rule, "edit_file", map[string]any{"file_path": "final_eino_adk/main.go"}) {
		t.Fatal("Edit(*.go) should match Go file paths")
	}
	if ruleMatches(rule, "edit_file", map[string]any{"file_path": "README.md"}) {
		t.Fatal("Edit(*.go) should not match markdown file paths")
	}
}

func newTestMiddleware(config Config) *permissionMiddleware {
	return newTestMiddlewareWithPrompt(config, func(string) (string, bool) {
		return "n", true
	})
}

func newTestMiddlewareWithPrompt(config Config, prompt PromptFunc) *permissionMiddleware {
	return NewWithConfig(prompt, config).(*permissionMiddleware)
}

func writePermissionFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
