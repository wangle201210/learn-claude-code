package todo

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestMiddlewareAddsWriteTodosToolAndInstruction(t *testing.T) {
	mw, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}

	_, runCtx, err := mw.BeforeAgent(context.Background(), &adk.ChatModelAgentContext{Instruction: "base"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runCtx.Instruction, "write_todos") {
		t.Fatalf("instruction = %q, want write_todos guidance", runCtx.Instruction)
	}
	if len(runCtx.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(runCtx.Tools))
	}
	info, err := runCtx.Tools[0].Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "write_todos" {
		t.Fatalf("tool name = %q, want write_todos", info.Name)
	}
}

func TestWriteTodosStoresSessionValueAndState(t *testing.T) {
	state := &State{}
	capture := &sessionValueCapture{}
	agent, err := newTodoTestAgent(context.Background(), state, capture, schema.AssistantMessage("", []schema.ToolCall{{
		ID:   "call-write-todos",
		Type: "function",
		Function: schema.FunctionCall{
			Name:      "write_todos",
			Arguments: `{"todos":[{"content":"Read code","activeForm":"Reading code","status":"in_progress"},{"content":"Run tests","activeForm":"Running tests","status":"pending"}]}`,
		},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	runTodoTestAgent(t, context.Background(), agent)

	want := []deep.TODO{
		{Content: "Read code", ActiveForm: "Reading code", Status: "in_progress"},
		{Content: "Run tests", ActiveForm: "Running tests", Status: "pending"},
	}
	assertTodos(t, state.Snapshot(), want)
	assertTodos(t, capture.snapshot(), want)
}

func TestWriteTodosClearsAllCompletedTodos(t *testing.T) {
	state := &State{}
	capture := &sessionValueCapture{}
	agent, err := newTodoTestAgent(context.Background(), state, capture, schema.AssistantMessage("", []schema.ToolCall{{
		ID:   "call-write-todos",
		Type: "function",
		Function: schema.FunctionCall{
			Name:      "write_todos",
			Arguments: `{"todos":[{"content":"Done","activeForm":"Done","status":"completed"}]}`,
		},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	runTodoTestAgent(t, context.Background(), agent)

	if got := state.Snapshot(); len(got) != 0 {
		t.Fatalf("state todos = %#v, want empty", got)
	}
	if got := capture.snapshot(); len(got) != 0 {
		t.Fatalf("session todos = %#v, want empty", got)
	}
}

func TestWriteTodosRejectsMultipleInProgressItems(t *testing.T) {
	state := &State{}
	mw, err := New(state)
	if err != nil {
		t.Fatal(err)
	}
	_, runCtx, err := mw.BeforeAgent(context.Background(), &adk.ChatModelAgentContext{})
	if err != nil {
		t.Fatal(err)
	}
	writeTodos := runCtx.Tools[0].(tool.InvokableTool)

	_, err = writeTodos.InvokableRun(context.Background(), `{"todos":[{"content":"A","status":"in_progress"},{"content":"B","status":"in_progress"}]}`)
	if err == nil {
		t.Fatal("expected multiple in_progress error")
	}
	if !strings.Contains(err.Error(), "only one todo") {
		t.Fatalf("error = %v, want only one todo", err)
	}
}

func newTodoTestAgent(ctx context.Context, state *State, capture *sessionValueCapture, first *schema.Message) (*adk.ChatModelAgent, error) {
	todoMW, err := New(state)
	if err != nil {
		return nil, err
	}
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          "todo-test",
		Description:   "todo test agent",
		Instruction:   "test",
		Model:         &todoTestModel{first: first},
		MaxIterations: 3,
		Handlers: []adk.ChatModelAgentMiddleware{
			todoMW,
			capture,
		},
	})
}

func runTodoTestAgent(t *testing.T, ctx context.Context, agent *adk.ChatModelAgent) {
	t.Helper()
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	iter := runner.Run(ctx, []adk.Message{schema.UserMessage("start")})
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			t.Fatal(event.Err)
		}
	}
}

type sessionValueCapture struct {
	*adk.BaseChatModelAgentMiddleware
	mu    sync.Mutex
	todos []deep.TODO
}

func (c *sessionValueCapture) AfterAgent(ctx context.Context, _ *adk.ChatModelAgentState) (context.Context, error) {
	value, ok := adk.GetSessionValue(ctx, deep.SessionKeyTodos)
	if !ok {
		return ctx, fmt.Errorf("missing todo session value")
	}
	todos, ok := value.([]deep.TODO)
	if !ok {
		return ctx, fmt.Errorf("todo session value type %T", value)
	}
	c.mu.Lock()
	c.todos = append([]deep.TODO(nil), todos...)
	c.mu.Unlock()
	return ctx, nil
}

func (c *sessionValueCapture) snapshot() []deep.TODO {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]deep.TODO(nil), c.todos...)
}

type todoTestModel struct {
	mu        sync.Mutex
	first     *schema.Message
	generated int
}

func (m *todoTestModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.generated++
	if m.generated == 1 {
		return m.first, nil
	}
	return schema.AssistantMessage("done", nil), nil
}

func (m *todoTestModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *todoTestModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func assertTodos(t *testing.T, got, want []deep.TODO) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("todos = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("todos = %#v, want %#v", got, want)
		}
	}
}
