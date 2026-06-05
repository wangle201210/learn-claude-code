package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	agentapp "github.com/wangle201210/learn-claude-code/final_eino_adk/internal/agent"
)

func TestFinalAgentCarriesHistoryWithoutDuplicatingSystemMessages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fake := newFinalTestModel()
	history := &conversationHistory{}
	compactController := agentapp.NewCompactController()
	agent, _, err := agentapp.Build(ctx, fake, nil, denyPrompt, history.replace, compactController)
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})

	runUserTurn(t, ctx, runner, history, "first")
	runUserTurn(t, ctx, runner, history, "second")

	inputs := fake.agentInputs()
	if len(inputs) != 2 {
		t.Fatalf("agent model calls = %d, want 2", len(inputs))
	}
	assertRoleContents(t, inputs[1],
		roleContent{role: schema.System},
		roleContent{role: schema.User, content: "first"},
		roleContent{role: schema.Assistant, content: "agent reply 1"},
		roleContent{role: schema.User, content: "second"},
	)
	if got := countRole(inputs[1], schema.System); got != 1 {
		t.Fatalf("second turn system messages = %d, want 1", got)
	}

	stored := history.copyMessages()
	if got := countRole(stored, schema.System); got != 0 {
		t.Fatalf("stored history system messages = %d, want 0", got)
	}
	if fake.memoryCalls() != 0 {
		t.Fatalf("ordinary conversation memory model calls = %d, want 0", fake.memoryCalls())
	}
	assertRoleContents(t, stored,
		roleContent{role: schema.User, content: "first"},
		roleContent{role: schema.Assistant, content: "agent reply 1"},
		roleContent{role: schema.User, content: "second"},
		roleContent{role: schema.Assistant, content: "agent reply 2"},
	)
	assertToolsInclude(t, fake.agentTools(), "compact", "task", "teammate", "background_execute", "create_worktree")
}

func TestFinalAgentManualCompactRunsOfficialSummarization(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fake := newFinalTestModel()
	history := &conversationHistory{}
	compactController := agentapp.NewCompactController()
	agent, _, err := agentapp.Build(ctx, fake, nil, denyPrompt, history.replace, compactController)
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})

	runUserTurn(t, ctx, runner, history, "first")
	runManualCompactTurn(t, ctx, runner, history, compactController)

	if fake.summaryCalls() != 1 {
		t.Fatalf("summary calls = %d, want 1", fake.summaryCalls())
	}
	inputs := fake.agentInputs()
	if len(inputs) != 2 {
		t.Fatalf("agent model calls = %d, want original turn + compact confirmation", len(inputs))
	}
	assertRoleContents(t, inputs[1],
		roleContent{role: schema.System},
		roleContent{role: schema.User, contains: "summary: first"},
		roleContent{role: schema.User, content: compactConfirmationPrompt},
	)

	stored := history.copyMessages()
	assertRoleContents(t, stored, roleContent{role: schema.User, contains: "summary: first"})
	if got := countRole(stored, schema.Assistant); got != 0 {
		t.Fatalf("stored compact confirmation assistant messages = %d, want 0", got)
	}
}

const compactConfirmationPrompt = "Context compaction is complete. Reply in one short Chinese sentence confirming the conversation history was compacted."

func runUserTurn(t *testing.T, ctx context.Context, runner *adk.Runner, history *conversationHistory, query string) {
	t.Helper()
	history.beginRound()
	input, userMessage := history.nextInput(query)
	runAgent(ctx, runner, history, input, userMessage, "test")
}

func runManualCompactTurn(t *testing.T, ctx context.Context, runner *adk.Runner, history *conversationHistory, controller *agentapp.CompactController) {
	t.Helper()
	history.beginRound()
	input := history.copyMessages()
	if len(input) == 0 {
		t.Fatal("manual compact needs existing history")
	}
	controller.RequestWithPrompt(compactConfirmationPrompt)
	runAgent(ctx, runner, history, input, nil, "test compact")
}

func denyPrompt(string) (string, bool) {
	return "n", true
}

type finalTestModel struct {
	mu        sync.Mutex
	responses int
	summaries int
	memories  int
	inputs    [][]*schema.Message
	tools     [][]string
}

func newFinalTestModel() *finalTestModel {
	return &finalTestModel{}
}

func (m *finalTestModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return m.respond(ctx, input, opts...)
}

func (m *finalTestModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.respond(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *finalTestModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *finalTestModel) respond(_ context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if isSummaryPrompt(input) {
		m.mu.Lock()
		m.summaries++
		m.mu.Unlock()
		return schema.AssistantMessage("summary: first", nil), nil
	}
	if isMemoryPrompt(input) {
		m.mu.Lock()
		m.memories++
		m.mu.Unlock()
		return schema.AssistantMessage("[]", nil), nil
	}

	common := model.GetCommonOptions(nil, opts...)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses++
	m.inputs = append(m.inputs, visibleTestMessages(input))
	m.tools = append(m.tools, toolNames(common.Tools))
	msg := schema.AssistantMessage(fmt.Sprintf("agent reply %d", m.responses), nil)
	msg.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{TotalTokens: 10}}
	return msg, nil
}

func (m *finalTestModel) agentInputs() [][]*schema.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]*schema.Message, len(m.inputs))
	for i, input := range m.inputs {
		out[i] = cloneTestMessages(input)
	}
	return out
}

func (m *finalTestModel) agentTools() [][]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]string, len(m.tools))
	for i, tools := range m.tools {
		out[i] = append([]string(nil), tools...)
	}
	return out
}

func (m *finalTestModel) summaryCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.summaries
}

func (m *finalTestModel) memoryCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.memories
}

func isSummaryPrompt(input []*schema.Message) bool {
	if len(input) == 0 {
		return false
	}
	last := input[len(input)-1]
	return last != nil && strings.Contains(last.Content, "Your task is to create a detailed summary")
}

func isMemoryPrompt(input []*schema.Message) bool {
	if len(input) == 0 {
		return false
	}
	content := input[len(input)-1].Content
	return strings.Contains(content, "Extract durable user preferences") ||
		strings.Contains(content, "Consolidate the following memory files")
}

func visibleTestMessages(messages []*schema.Message) []*schema.Message {
	out := make([]*schema.Message, 0, len(messages))
	for _, msg := range cloneTestMessages(messages) {
		if hasTestExtra(msg, "final_eino_memory_context") || hasTestExtra(msg, "__agentsmd_content__") {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func hasTestExtra(msg *schema.Message, key string) bool {
	if msg == nil || msg.Extra == nil {
		return false
	}
	_, ok := msg.Extra[key]
	return ok
}

func cloneTestMessages(messages []*schema.Message) []*schema.Message {
	out := make([]*schema.Message, len(messages))
	for i, msg := range messages {
		if msg == nil {
			continue
		}
		copied := *msg
		if msg.ToolCalls != nil {
			copied.ToolCalls = append([]schema.ToolCall(nil), msg.ToolCalls...)
		}
		if msg.Extra != nil {
			copied.Extra = make(map[string]any, len(msg.Extra))
			for k, v := range msg.Extra {
				copied.Extra[k] = v
			}
		}
		out[i] = &copied
	}
	return out
}

func toolNames(infos []*schema.ToolInfo) []string {
	out := make([]string, 0, len(infos))
	for _, info := range infos {
		if info != nil {
			out = append(out, info.Name)
		}
	}
	return out
}

type roleContent struct {
	role     schema.RoleType
	content  string
	contains string
}

func assertRoleContents(t *testing.T, messages []*schema.Message, want ...roleContent) {
	t.Helper()
	if len(messages) != len(want) {
		t.Fatalf("message length = %d, want %d\nmessages:\n%s", len(messages), len(want), formatMessages(messages))
	}
	for i, expected := range want {
		msg := messages[i]
		if msg == nil {
			t.Fatalf("message %d is nil", i)
		}
		if msg.Role != expected.role {
			t.Fatalf("message %d role = %q, want %q\nmessages:\n%s", i, msg.Role, expected.role, formatMessages(messages))
		}
		if expected.content != "" && msg.Content != expected.content {
			t.Fatalf("message %d content = %q, want %q\nmessages:\n%s", i, msg.Content, expected.content, formatMessages(messages))
		}
		if expected.contains != "" && !strings.Contains(msg.Content, expected.contains) {
			t.Fatalf("message %d content = %q, want containing %q\nmessages:\n%s", i, msg.Content, expected.contains, formatMessages(messages))
		}
	}
}

func countRole(messages []*schema.Message, role schema.RoleType) int {
	count := 0
	for _, msg := range messages {
		if msg != nil && msg.Role == role {
			count++
		}
	}
	return count
}

func assertToolsInclude(t *testing.T, toolsPerCall [][]string, names ...string) {
	t.Helper()
	if len(toolsPerCall) == 0 {
		t.Fatal("no tool list captured")
	}
	seen := map[string]bool{}
	for _, name := range toolsPerCall[0] {
		seen[name] = true
	}
	for _, name := range names {
		if !seen[name] {
			t.Fatalf("tool %q not found in first model call tools: %v", name, toolsPerCall[0])
		}
	}
}

func formatMessages(messages []*schema.Message) string {
	var b strings.Builder
	for i, msg := range messages {
		if msg == nil {
			fmt.Fprintf(&b, "%d: <nil>\n", i)
			continue
		}
		fmt.Fprintf(&b, "%d: %s %q\n", i, msg.Role, msg.Content)
	}
	return b.String()
}
