package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	agentapp "github.com/wangle201210/learn-claude-code/final_eino_adk/internal/agent"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/recovery"
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
	assertToolsInclude(t, fake.agentTools(), "compact", "task", "teammate", "write_todos", "background_execute", "spawn_teammate", "create_worktree")
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
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: true})

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

func TestFinalAgentReactiveCompactOnPromptTooLong(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fake := newFinalTestModel()
	fake.enqueueErrors(errors.New("context_length_exceeded: too many tokens"))
	history := &conversationHistory{}
	compactController := agentapp.NewCompactController()
	agent, _, err := agentapp.Build(ctx, fake, nil, denyPrompt, history.replace, compactController)
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})

	history.beginRound()
	input, userMessage := history.nextInput("please continue")
	result := runAgentWithRecovery(ctx, runner, history, compactController, input, userMessage, "test")

	if result.Err != nil {
		t.Fatalf("run err = %v, want recovered", result.Err)
	}
	if fake.summaryCalls() != 1 {
		t.Fatalf("summary calls = %d, want 1", fake.summaryCalls())
	}
	inputs := fake.agentInputs()
	if len(inputs) != 2 {
		t.Fatalf("agent model calls = %d, want original failed call + compact retry", len(inputs))
	}
	assertRoleContents(t, inputs[1],
		roleContent{role: schema.System},
		roleContent{role: schema.User, contains: "summary: first"},
	)
	stored := history.copyMessages()
	assertRoleContents(t, stored,
		roleContent{role: schema.User, contains: "summary: first"},
		roleContent{role: schema.Assistant, content: "agent reply 2"},
	)
}

func TestFinalAgentContinuesAfterMaxTokensWithOfficialRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fake := newFinalTestModel()
	fake.enqueueResponses(truncatedResponse("partial answer"))
	history := &conversationHistory{}
	compactController := agentapp.NewCompactController()
	agent, _, err := agentapp.Build(ctx, fake, nil, denyPrompt, history.replace, compactController)
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})

	runUserTurn(t, ctx, runner, history, "long answer please")

	inputs := fake.agentInputs()
	if len(inputs) != 2 {
		t.Fatalf("agent model calls = %d, want original + continuation retry", len(inputs))
	}
	if !messageContentsContain(inputs[1], "partial answer") {
		t.Fatalf("retry input missing partial assistant answer:\n%s", formatMessages(inputs[1]))
	}
	if !messageContentsContain(inputs[1], recovery.ContinuationPrompt) {
		t.Fatalf("retry input missing continuation prompt:\n%s", formatMessages(inputs[1]))
	}
	stored := history.copyMessages()
	if messageContentsContain(stored, recovery.ContinuationPrompt) {
		t.Fatalf("recovery continuation prompt leaked into stored history:\n%s", formatMessages(stored))
	}
	if !messageContentsContain(stored, "agent reply 2") {
		t.Fatalf("stored history missing recovered answer:\n%s", formatMessages(stored))
	}
}

func TestFinalAgentContinuesAfterToolError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fake := newFinalTestModel()
	fake.enqueueResponses(
		responseWithToolCall("call-grep", "grep", `{"pattern":"[","path":".","output_mode":"content"}`),
		schema.AssistantMessage("continued after tool error", nil),
	)
	history := &conversationHistory{}
	compactController := agentapp.NewCompactController()
	agent, _, err := agentapp.Build(ctx, fake, nil, denyPrompt, history.replace, compactController)
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})

	history.beginRound()
	input, userMessage := history.nextInput("search with a bad regex")
	result := runAgent(ctx, runner, history, input, userMessage, "test tool error")

	if result.Err != nil {
		t.Fatalf("run err = %v, want recovered tool error", result.Err)
	}
	inputs := fake.agentInputs()
	if len(inputs) != 2 {
		t.Fatalf("agent model calls = %d, want tool-call turn + recovery turn", len(inputs))
	}
	if !messageContentsContain(inputs[1], "Tool error from grep") {
		t.Fatalf("second model input missing tool error feedback:\n%s", formatMessages(inputs[1]))
	}
	if !messageContentsContain(history.copyMessages(), "continued after tool error") {
		t.Fatalf("stored history missing final answer:\n%s", formatMessages(history.copyMessages()))
	}
}

func TestRunAgentReportsMissingEnvWhenModelProducesNoOutput(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OPENAI_BASE_URL", "")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fake := newFinalTestModel()
	fake.enqueueResponses(schema.AssistantMessage("", nil))
	history := &conversationHistory{}
	compactController := agentapp.NewCompactController()
	agent, _, err := agentapp.Build(ctx, fake, nil, denyPrompt, history.replace, compactController)
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})

	history.beginRound()
	input, userMessage := history.nextInput("hi")
	var result agentRunResult
	output := captureStdout(t, func() {
		result = runAgent(ctx, runner, history, input, userMessage, "test")
	})

	if result.Err == nil {
		t.Fatal("runAgent should report empty model output as an error")
	}
	if !strings.Contains(output, "模型没有返回内容") {
		t.Fatalf("output %q should explain empty model output", output)
	}
	for _, want := range []string{"OPENAI_API_KEY", "OPENAI_MODEL", "OPENAI_BASE_URL"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output %q should mention missing %s", output, want)
		}
	}
}

func TestFinalAgentRunsUserPromptSubmitHookAsTransientContext(t *testing.T) {
	t.Setenv("FINAL_EINO_HOOKS", `{
		"hooks": {
			"UserPromptSubmit": [
				{"hooks":[{"type":"command","command":"printf '{\"hookSpecificOutput\":{\"hookEventName\":\"UserPromptSubmit\",\"additionalContext\":\"hook says inspect workspace\"}}'"}]}
			]
		}
	}`)
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

	runUserTurn(t, ctx, runner, history, "hello")

	inputs := fake.agentInputs()
	if len(inputs) != 1 {
		t.Fatalf("agent model calls = %d, want 1", len(inputs))
	}
	if !messageContentsContain(inputs[0], "hook says inspect workspace") {
		t.Fatalf("model input missing hook context:\n%s", formatMessages(inputs[0]))
	}
	if messageContentsContain(history.copyMessages(), "hook says inspect workspace") {
		t.Fatalf("hook context leaked into stored history:\n%s", formatMessages(history.copyMessages()))
	}
}

func TestFinalAgentSpawnTeammateProducesNotification(t *testing.T) {
	t.Setenv("FINAL_EINO_TEAMMATE_IDLE_MS", "0")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fake := newFinalTestModel()
	fake.enqueueResponses(responseWithToolCall("call-spawn", "spawn_teammate", `{"name":"reviewer","role":"reviewer","prompt":"report done"}`))
	history := &conversationHistory{}
	compactController := agentapp.NewCompactController()
	agent, runtimeState, err := agentapp.Build(ctx, fake, nil, denyPrompt, history.replace, compactController)
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})

	runUserTurn(t, ctx, runner, history, "start teammate")

	notes := waitNotifications(t, runtimeState, 3*time.Second, "teammate_result")
	if !messageTextsContain(notes, "agent reply") {
		t.Fatalf("teammate notification missing result text: %v", notes)
	}
}

func TestFinalAgentRoutesSpawnedTeammateModelCall(t *testing.T) {
	t.Setenv("FINAL_EINO_TEAMMATE_IDLE_MS", "0")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fake := newFinalTestModel()
	fake.enqueueResponses(responseWithToolCall("call-spawn", "spawn_teammate", `{"name":"debugger","role":"debugger","prompt":"debug a race condition in memory compact"}`))
	routed := newRoutedModel(fake, modelRouterConfig{
		defaultModel:  "standard-model",
		simpleModel:   "simple-model",
		standardModel: "standard-model",
		complexModel:  "complex-model",
	})
	history := &conversationHistory{}
	compactController := agentapp.NewCompactController()
	agent, runtimeState, err := agentapp.Build(ctx, routed, nil, denyPrompt, history.replace, compactController)
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})

	runUserTurn(t, ctx, runner, history, "start teammate")

	waitNotifications(t, runtimeState, 3*time.Second, "teammate_result")
	assertModelsContain(t, fake.modelSelections(), "complex-model")
}

func TestFinalAgentRoutesModelTiersThroughRealMiddleware(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fake := newFinalTestModel()
	routed := newRoutedModel(fake, modelRouterConfig{
		defaultModel:  "complex-model",
		simpleModel:   "simple-model",
		standardModel: "standard-model",
		complexModel:  "complex-model",
	})
	history := &conversationHistory{}
	compactController := agentapp.NewCompactController()
	agent, _, err := agentapp.Build(ctx, routed, nil, denyPrompt, history.replace, compactController)
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})

	runUserTurn(t, ctx, runner, history, "hello")
	runUserTurn(t, ctx, runner, history, "implement a small fix in foo.go")
	runManualCompactTurn(t, ctx, runner, history, compactController)

	if fake.summaryCalls() != 1 {
		t.Fatalf("summary calls = %d, want 1", fake.summaryCalls())
	}
	assertModelSequence(t, fake.modelSelections(),
		"simple-model",
		"standard-model",
		"standard-model",
		"simple-model",
	)
}

func TestFinalAgentRoutesToolRequestedCompactWithoutConfirmation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fake := newFinalTestModel()
	routed := newRoutedModel(fake, modelRouterConfig{
		defaultModel:  "complex-model",
		simpleModel:   "simple-model",
		standardModel: "standard-model",
		complexModel:  "complex-model",
	})
	history := &conversationHistory{}
	compactController := agentapp.NewCompactController()
	agent, _, err := agentapp.Build(ctx, routed, nil, denyPrompt, history.replace, compactController)
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})

	runUserTurn(t, ctx, runner, history, "hello")
	runControllerCompactTurn(t, ctx, runner, history, compactController)

	if fake.summaryCalls() != 1 {
		t.Fatalf("summary calls = %d, want 1", fake.summaryCalls())
	}
	assertModelSequence(t, fake.modelSelections(),
		"simple-model",
		"standard-model",
		"standard-model",
	)
}

func TestFinalAgentRoutesDelegatedTaskModelCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fake := newFinalTestModel()
	fake.enqueueResponses(
		responseWithToolCall("call-task", "task", `{"request":"implement a small fix in foo.go"}`),
		schema.AssistantMessage("task result", nil),
	)
	routed := newRoutedModel(fake, modelRouterConfig{
		defaultModel:  "complex-model",
		simpleModel:   "simple-model",
		standardModel: "standard-model",
		complexModel:  "complex-model",
	})
	history := &conversationHistory{}
	compactController := agentapp.NewCompactController()
	agent, _, err := agentapp.Build(ctx, routed, nil, denyPrompt, history.replace, compactController)
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})

	runUserTurn(t, ctx, runner, history, "delegate this small coding fix")

	assertModelSequence(t, fake.modelSelections(),
		"standard-model",
		"standard-model",
		"standard-model",
	)
}

func TestFinalAgentRoutesAsyncMemoryExtraction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fake := newFinalTestModel()
	routed := newRoutedModel(fake, modelRouterConfig{
		defaultModel:  "complex-model",
		simpleModel:   "simple-model",
		standardModel: "standard-model",
		complexModel:  "complex-model",
	})
	history := &conversationHistory{}
	compactController := agentapp.NewCompactController()
	agent, _, err := agentapp.Build(ctx, routed, nil, denyPrompt, history.replace, compactController)
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})

	runUserTurn(t, ctx, runner, history, "请记住我以后希望回答更简洁")

	if !fake.waitMemoryCalls(1, time.Second) {
		t.Fatal("async memory extraction was not routed through model")
	}
	assertModelPrefix(t, fake.modelSelections(),
		"simple-model",
		"standard-model",
	)
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

func runControllerCompactTurn(t *testing.T, ctx context.Context, runner *adk.Runner, history *conversationHistory, controller *agentapp.CompactController) {
	t.Helper()
	history.beginRound()
	input := history.copyMessages()
	if len(input) == 0 {
		t.Fatal("controller compact needs existing history")
	}
	controller.Request()
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
	models    []string
	scripted  []finalTestModelScript
}

type finalTestModelScript struct {
	message *schema.Message
	err     error
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
	m.recordModel(opts...)
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
	if len(m.scripted) > 0 {
		item := m.scripted[0]
		m.scripted = m.scripted[1:]
		if item.err != nil {
			return nil, item.err
		}
		return cloneTestMessage(item.message), nil
	}
	msg := schema.AssistantMessage(fmt.Sprintf("agent reply %d", m.responses), nil)
	msg.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{TotalTokens: 10}}
	return msg, nil
}

func (m *finalTestModel) enqueueResponses(messages ...*schema.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range messages {
		m.scripted = append(m.scripted, finalTestModelScript{message: cloneTestMessage(msg)})
	}
}

func (m *finalTestModel) enqueueErrors(errs ...error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, err := range errs {
		m.scripted = append(m.scripted, finalTestModelScript{err: err})
	}
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

func (m *finalTestModel) waitMemoryCalls(want int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m.memoryCalls() >= want {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func (m *finalTestModel) modelSelections() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.models...)
}

func (m *finalTestModel) recordModel(opts ...model.Option) {
	common := model.GetCommonOptions(nil, opts...)
	selected := ""
	if common.Model != nil {
		selected = *common.Model
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.models = append(m.models, selected)
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
		out[i] = cloneTestMessage(msg)
	}
	return out
}

func cloneTestMessage(msg *schema.Message) *schema.Message {
	if msg == nil {
		return nil
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
	return &copied
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

func messageContentsContain(messages []*schema.Message, needle string) bool {
	for _, msg := range messages {
		if msg != nil && strings.Contains(msg.Content, needle) {
			return true
		}
	}
	return false
}

func messageTextsContain(messages []string, needle string) bool {
	for _, msg := range messages {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func waitNotifications(t *testing.T, runtimeState interface{ CollectNotifications() []string }, timeout time.Duration, needle string) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		notes := runtimeState.CollectNotifications()
		if messageTextsContain(notes, needle) {
			return notes
		}
		time.Sleep(5 * time.Millisecond)
	}
	return runtimeState.CollectNotifications()
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

func assertModelSequence(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("model selections = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("model selections = %v, want %v", got, want)
		}
	}
}

func assertModelPrefix(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) < len(want) {
		t.Fatalf("model selections = %v, want prefix %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("model selections = %v, want prefix %v", got, want)
		}
	}
}

func assertModelsContain(t *testing.T, got []string, want string) {
	t.Helper()
	for _, modelName := range got {
		if modelName == want {
			return
		}
	}
	t.Fatalf("model selections = %v, want to contain %q", got, want)
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = old
	}()

	fn()

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	return string(out)
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

func responseWithToolCall(id, name, args string) *schema.Message {
	return schema.AssistantMessage("", []schema.ToolCall{{
		ID:   id,
		Type: "function",
		Function: schema.FunctionCall{
			Name:      name,
			Arguments: args,
		},
	}})
}

func truncatedResponse(content string) *schema.Message {
	msg := schema.AssistantMessage(content, nil)
	msg.ResponseMeta = &schema.ResponseMeta{FinishReason: "max_tokens"}
	return msg
}
