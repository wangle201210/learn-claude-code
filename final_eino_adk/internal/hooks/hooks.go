package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const (
	ContextExtraKey    = "final_eino_hook_context"
	defaultTimeout     = 60 * time.Second
	maxCommandOutput   = 20_000
	envHooksConfigName = "FINAL_EINO_HOOKS"
)

type Settings struct {
	Hooks map[string][]Matcher `json:"hooks"`
}

type Matcher struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []HookCommand `json:"hooks"`
}

type HookCommand struct {
	Type    string  `json:"type"`
	Command string  `json:"command"`
	If      string  `json:"if,omitempty"`
	Timeout float64 `json:"timeout,omitempty"`
	Once    bool    `json:"once,omitempty"`
}

type middleware struct {
	*adk.BaseChatModelAgentMiddleware
	root        string
	settings    Settings
	seenPrompts map[string]bool
	usedOnce    map[string]bool
	mu          sync.Mutex
}

type hookEvent struct {
	Name         string        `json:"hook_event_name"`
	CWD          string        `json:"cwd,omitempty"`
	Prompt       string        `json:"prompt,omitempty"`
	ToolName     string        `json:"tool_name,omitempty"`
	ToolInput    any           `json:"tool_input,omitempty"`
	ToolResponse any           `json:"tool_response,omitempty"`
	ToolUseID    string        `json:"tool_use_id,omitempty"`
	Messages     []messageView `json:"messages,omitempty"`
}

type messageView struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type hookResult struct {
	Blocked           bool
	Reason            string
	AdditionalContext string
	Output            string
}

type hookJSONOutput struct {
	Continue           *bool              `json:"continue,omitempty"`
	Decision           string             `json:"decision,omitempty"`
	Reason             string             `json:"reason,omitempty"`
	StopReason         string             `json:"stopReason,omitempty"`
	SystemMessage      string             `json:"systemMessage,omitempty"`
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

type hookSpecificOutput struct {
	HookEventName            string         `json:"hookEventName,omitempty"`
	AdditionalContext        string         `json:"additionalContext,omitempty"`
	PermissionDecision       string         `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string         `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             map[string]any `json:"updatedInput,omitempty"`
}

func Load(root string) (adk.ChatModelAgentMiddleware, error) {
	settings, err := loadSettings(root)
	if err != nil {
		return nil, err
	}
	if len(settings.Hooks) == 0 {
		return nil, nil
	}
	return New(root, settings), nil
}

func New(root string, settings Settings) adk.ChatModelAgentMiddleware {
	return &middleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		root:                         root,
		settings:                     settings,
		seenPrompts:                  map[string]bool{},
		usedOnce:                     map[string]bool{},
	}
}

func (m *middleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	key, prompt := latestUserPromptKey(state.Messages)
	if key == "" || prompt == "" {
		return ctx, state, nil
	}
	if m.promptSeen(key) {
		return ctx, state, nil
	}
	event := hookEvent{
		Name:   "UserPromptSubmit",
		CWD:    m.root,
		Prompt: prompt,
	}
	results, err := m.run(ctx, event, "")
	if err != nil {
		return nil, nil, err
	}
	contextText := collectAdditionalContext(results)
	if contextText == "" {
		return ctx, state, nil
	}
	next := *state
	next.Messages = append(append([]adk.Message(nil), state.Messages...), hookContextMessage(contextText))
	return ctx, &next, nil
}

func (m *middleware) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
		toolName, callID := toolContextValues(tCtx)
		toolInput := parseJSONValue(argumentsInJSON)
		preResults, err := m.run(ctx, hookEvent{
			Name:      "PreToolUse",
			CWD:       m.root,
			ToolName:  toolName,
			ToolInput: toolInput,
			ToolUseID: callID,
		}, toolName)
		if err != nil {
			return "", err
		}
		if block := firstBlock(preResults); block != "" {
			return "Hook blocked PreToolUse: " + block, nil
		}

		output, err := endpoint(ctx, argumentsInJSON, opts...)
		if err != nil {
			return output, err
		}

		postResults, err := m.run(ctx, hookEvent{
			Name:         "PostToolUse",
			CWD:          m.root,
			ToolName:     toolName,
			ToolInput:    toolInput,
			ToolResponse: output,
			ToolUseID:    callID,
		}, toolName)
		if err != nil {
			return "", err
		}
		if block := firstBlock(postResults); block != "" {
			return output + "\n\nHook blocked PostToolUse: " + block, nil
		}
		return output, nil
	}, nil
}

func (m *middleware) AfterAgent(ctx context.Context, state *adk.ChatModelAgentState) (context.Context, error) {
	results, err := m.run(ctx, hookEvent{
		Name:     "Stop",
		CWD:      m.root,
		Messages: messageViews(state.Messages),
	}, "")
	if err != nil {
		return nil, err
	}
	if block := firstBlock(results); block != "" {
		return nil, errors.New("Hook blocked Stop: " + block)
	}
	return ctx, nil
}

func (m *middleware) run(ctx context.Context, event hookEvent, matchQuery string) ([]hookResult, error) {
	matches := m.matching(event, matchQuery)
	if len(matches) == 0 {
		return nil, nil
	}
	var results []hookResult
	for _, matched := range matches {
		if matched.command.Type != "command" || strings.TrimSpace(matched.command.Command) == "" {
			continue
		}
		if matched.command.Once && m.onceUsed(matched.key) {
			continue
		}
		result, err := runCommandHook(ctx, m.root, event, matched.command)
		if err != nil {
			return nil, err
		}
		if matched.command.Once {
			m.markOnceUsed(matched.key)
		}
		results = append(results, result)
	}
	return results, nil
}

type matchedHook struct {
	key     string
	command HookCommand
}

func (m *middleware) matching(event hookEvent, matchQuery string) []matchedHook {
	matchers := m.settings.Hooks[event.Name]
	var out []matchedHook
	for i, matcher := range matchers {
		if !matchesMatcher(matcher.Matcher, matchQuery) {
			continue
		}
		for j, hook := range matcher.Hooks {
			if hook.If != "" && !matchesIfCondition(hook.If, event, matchQuery) {
				continue
			}
			out = append(out, matchedHook{
				key:     fmt.Sprintf("%s:%d:%d", event.Name, i, j),
				command: hook,
			})
		}
	}
	return out
}

func runCommandHook(ctx context.Context, root string, event hookEvent, hook HookCommand) (hookResult, error) {
	timeout := defaultTimeout
	if hook.Timeout > 0 {
		timeout = time.Duration(hook.Timeout * float64(time.Second))
	}
	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(hookCtx, "bash", "-c", hook.Command)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "FINAL_EINO_HOOK_EVENT="+event.Name)
	input, err := json.Marshal(event)
	if err != nil {
		return hookResult{}, err
	}
	cmd.Stdin = bytes.NewReader(append(input, '\n'))
	out, err := cmd.CombinedOutput()
	result := parseHookOutput(out)
	if hookCtx.Err() == context.DeadlineExceeded {
		result.Blocked = true
		result.Reason = "hook timed out"
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
			result.Blocked = true
			if result.Reason == "" {
				result.Reason = strings.TrimSpace(string(out))
			}
		} else if result.Reason == "" {
			result.Reason = strings.TrimSpace(string(out))
		}
	}
	if result.Output == "" {
		result.Output = truncateOutput(string(out))
	}
	if result.Blocked && result.Reason == "" {
		result.Reason = "hook blocked"
	}
	return result, nil
}

func parseHookOutput(out []byte) hookResult {
	text := strings.TrimSpace(string(out))
	result := hookResult{Output: truncateOutput(text)}
	if text == "" {
		return result
	}
	var parsed hookJSONOutput
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return result
	}
	if parsed.Continue != nil && !*parsed.Continue {
		result.Blocked = true
	}
	switch strings.ToLower(parsed.Decision) {
	case "block", "deny":
		result.Blocked = true
	}
	switch strings.ToLower(parsed.HookSpecificOutput.PermissionDecision) {
	case "deny", "block":
		result.Blocked = true
	}
	result.Reason = firstNonEmpty(parsed.Reason, parsed.StopReason, parsed.HookSpecificOutput.PermissionDecisionReason, parsed.SystemMessage)
	result.AdditionalContext = parsed.HookSpecificOutput.AdditionalContext
	return result
}

func loadSettings(root string) (Settings, error) {
	if raw := strings.TrimSpace(os.Getenv(envHooksConfigName)); raw != "" {
		return parseSettings([]byte(raw))
	}
	for _, path := range []string{
		filepath.Join(root, ".final_eino_hooks.json"),
		filepath.Join(root, ".claude", "settings.json"),
	} {
		data, err := os.ReadFile(path)
		if err == nil {
			return parseSettings(data)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return Settings{}, err
		}
	}
	return Settings{}, nil
}

func parseSettings(data []byte) (Settings, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return Settings{}, err
	}
	if rawHooks, ok := object["hooks"]; ok {
		var hooks map[string][]Matcher
		if err := json.Unmarshal(rawHooks, &hooks); err != nil {
			return Settings{}, err
		}
		return Settings{Hooks: hooks}, nil
	}
	hasHookEvent := false
	for name := range object {
		if isHookEventName(name) {
			hasHookEvent = true
			break
		}
	}
	if !hasHookEvent {
		return Settings{}, nil
	}
	var hooks map[string][]Matcher
	if err := json.Unmarshal(data, &hooks); err != nil {
		return Settings{}, err
	}
	return Settings{Hooks: hooks}, nil
}

func isHookEventName(name string) bool {
	switch name {
	case "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop":
		return true
	default:
		return false
	}
}

func latestUserPromptKey(messages []adk.Message) (string, string) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg == nil || msg.Role != schema.User || isTransientHookMessage(msg) {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		return fmt.Sprintf("%d:%s", i, content), content
	}
	return "", ""
}

func hookContextMessage(content string) adk.Message {
	msg := schema.UserMessage("<hook_context>\n" + content + "\n</hook_context>")
	msg.Extra = map[string]any{ContextExtraKey: true}
	return msg
}

func isTransientHookMessage(msg adk.Message) bool {
	if msg == nil || msg.Extra == nil {
		return false
	}
	_, ok := msg.Extra[ContextExtraKey]
	return ok
}

func matchesMatcher(pattern, query string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || pattern == "*" {
		return true
	}
	if query == "" {
		return false
	}
	if ok, _ := filepath.Match(strings.ToLower(pattern), strings.ToLower(query)); ok {
		return true
	}
	return strings.EqualFold(pattern, query)
}

func matchesIfCondition(condition string, event hookEvent, matchQuery string) bool {
	condition = strings.TrimSpace(condition)
	if condition == "" {
		return true
	}
	open := strings.Index(condition, "(")
	close := strings.LastIndex(condition, ")")
	if open <= 0 || close <= open {
		return strings.Contains(strings.ToLower(matchQuery), strings.ToLower(condition))
	}
	toolName := strings.TrimSpace(condition[:open])
	argPattern := strings.TrimSpace(condition[open+1 : close])
	if !matchesMatcher(toolName, event.ToolName) {
		return false
	}
	if argPattern == "" || argPattern == "*" {
		return true
	}
	raw, _ := json.Marshal(event.ToolInput)
	if ok, _ := filepath.Match(strings.ToLower(argPattern), strings.ToLower(string(raw))); ok {
		return true
	}
	return strings.Contains(strings.ToLower(string(raw)), strings.ToLower(strings.Trim(argPattern, "*")))
}

func parseJSONValue(raw string) any {
	var out any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return raw
	}
	return out
}

func toolContextValues(tCtx *adk.ToolContext) (string, string) {
	if tCtx == nil {
		return "", ""
	}
	return tCtx.Name, tCtx.CallID
}

func collectAdditionalContext(results []hookResult) string {
	var parts []string
	for _, result := range results {
		if text := strings.TrimSpace(result.AdditionalContext); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func firstBlock(results []hookResult) string {
	for _, result := range results {
		if result.Blocked {
			return firstNonEmpty(result.Reason, result.Output, "hook blocked")
		}
	}
	return ""
}

func messageViews(messages []adk.Message) []messageView {
	out := make([]messageView, 0, len(messages))
	for _, msg := range messages {
		if msg == nil || msg.Role == schema.System || isTransientHookMessage(msg) {
			continue
		}
		out = append(out, messageView{
			Role:    string(msg.Role),
			Content: msg.Content,
		})
	}
	return out
}

func (m *middleware) promptSeen(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seenPrompts[key] {
		return true
	}
	m.seenPrompts[key] = true
	return false
}

func (m *middleware) onceUsed(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usedOnce[key]
}

func (m *middleware) markOnceUsed(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usedOnce[key] = true
}

func truncateOutput(s string) string {
	if len(s) <= maxCommandOutput {
		return s
	}
	return s[:maxCommandOutput] + "\n... (truncated)"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
