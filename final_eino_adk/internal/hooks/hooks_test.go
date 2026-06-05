package hooks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestLoadReadsFinalHooksConfig(t *testing.T) {
	root := t.TempDir()
	err := os.WriteFile(filepath.Join(root, ".final_eino_hooks.json"), []byte(`{
		"hooks": {
			"PreToolUse": [
				{"matcher":"execute","hooks":[{"type":"command","command":"cat"}]}
			]
		}
	}`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	mw, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if mw == nil {
		t.Fatal("middleware should load hooks")
	}
}

func TestLoadIgnoresClaudeSettingsWithoutHooks(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := os.WriteFile(filepath.Join(root, ".claude", "settings.json"), []byte(`{
		"permissions": {"defaultMode": "default"}
	}`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	mw, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if mw != nil {
		t.Fatal("middleware should not load when settings has no hooks")
	}
}

func TestUserPromptSubmitInjectsAdditionalContextOnce(t *testing.T) {
	mw := New(t.TempDir(), Settings{Hooks: map[string][]Matcher{
		"UserPromptSubmit": {{
			Hooks: []HookCommand{{
				Type:    "command",
				Command: `printf '{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"remember hook context"}}'`,
			}},
		}},
	}}).(*middleware)
	state := &adk.ChatModelAgentState{Messages: []adk.Message{schema.UserMessage("hello")}}

	_, next, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Messages) != 2 {
		t.Fatalf("messages = %d, want user + hook context", len(next.Messages))
	}
	if !strings.Contains(next.Messages[1].Content, "remember hook context") {
		t.Fatalf("hook context message = %q", next.Messages[1].Content)
	}
	if _, ok := next.Messages[1].Extra[ContextExtraKey]; !ok {
		t.Fatalf("hook context extra missing: %#v", next.Messages[1].Extra)
	}

	_, again, err := mw.BeforeModelRewriteState(context.Background(), next, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Messages) != 2 {
		t.Fatalf("messages = %d, want hook context only once", len(again.Messages))
	}
}

func TestPreToolUseHookBlocksTool(t *testing.T) {
	mw := New(t.TempDir(), Settings{Hooks: map[string][]Matcher{
		"PreToolUse": {{
			Matcher: "execute",
			Hooks: []HookCommand{{
				Type:    "command",
				Command: `printf '{"decision":"block","reason":"no execute"}'`,
			}},
		}},
	}})
	wrapped, err := mw.WrapInvokableToolCall(context.Background(), func(context.Context, string, ...tool.Option) (string, error) {
		return "should not run", nil
	}, &adk.ToolContext{Name: "execute", CallID: "call-1"})
	if err != nil {
		t.Fatal(err)
	}

	out, err := wrapped(context.Background(), `{"command":"pwd"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Hook blocked PreToolUse: no execute") {
		t.Fatalf("output = %q", out)
	}
}

func TestPostToolUseHookRunsAfterTool(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "post.txt")
	mw := New(root, Settings{Hooks: map[string][]Matcher{
		"PostToolUse": {{
			Matcher: "execute",
			Hooks: []HookCommand{{
				Type:    "command",
				Command: `cat > post.txt`,
			}},
		}},
	}})
	wrapped, err := mw.WrapInvokableToolCall(context.Background(), func(context.Context, string, ...tool.Option) (string, error) {
		return "ok", nil
	}, &adk.ToolContext{Name: "execute", CallID: "call-1"})
	if err != nil {
		t.Fatal(err)
	}

	out, err := wrapped(context.Background(), `{"command":"pwd"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "ok" {
		t.Fatalf("output = %q, want ok", out)
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"hook_event_name":"PostToolUse"`) {
		t.Fatalf("post hook input = %q", raw)
	}
}

func TestStopHookCanBlock(t *testing.T) {
	mw := New(t.TempDir(), Settings{Hooks: map[string][]Matcher{
		"Stop": {{
			Hooks: []HookCommand{{
				Type:    "command",
				Command: `printf 'verification missing'; exit 2`,
			}},
		}},
	}})

	_, err := mw.AfterAgent(context.Background(), &adk.ChatModelAgentState{
		Messages: []adk.Message{schema.UserMessage("hello"), schema.AssistantMessage("done", nil)},
	})
	if err == nil {
		t.Fatal("expected stop hook block")
	}
	if !strings.Contains(err.Error(), "verification missing") {
		t.Fatalf("error = %v", err)
	}
}
