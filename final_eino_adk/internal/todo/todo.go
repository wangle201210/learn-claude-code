package todo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

const prompt = `

# write_todos

Use write_todos to maintain a structured task list for complex coding work.
- Use it for multi-step tasks, broad refactors, debugging with several hypotheses, or when the user gives multiple requirements.
- Update it as work progresses: keep at most one item in_progress, mark items completed as soon as they are done, and add verification when relevant.
- Do not use it for one-step fixes, simple questions, or purely conversational requests.
`

type State struct {
	mu    sync.Mutex
	todos []deep.TODO
}

type writeTodosArgs struct {
	Todos []deep.TODO `json:"todos"`
}

func New(state *State) (adk.ChatModelAgentMiddleware, error) {
	if state == nil {
		state = &State{}
	}
	t, err := buildTool(state)
	if err != nil {
		return nil, err
	}
	return &middleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		tool:                         t,
	}, nil
}

func (s *State) Snapshot() []deep.TODO {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]deep.TODO(nil), s.todos...)
}

func buildTool(state *State) (tool.BaseTool, error) {
	return utils.InferTool[*writeTodosArgs, string]("write_todos", "Create and update the current session todo list for multi-step coding work.", func(ctx context.Context, input *writeTodosArgs) (string, error) {
		if input == nil {
			return "", fmt.Errorf("missing todo input")
		}
		next, err := normalize(input.Todos)
		if err != nil {
			return "", err
		}
		if allCompleted(next) {
			next = []deep.TODO{}
		}
		state.mu.Lock()
		state.todos = append([]deep.TODO(nil), next...)
		state.mu.Unlock()

		adk.AddSessionValue(ctx, deep.SessionKeyTodos, append([]deep.TODO(nil), next...))
		encoded, err := json.Marshal(next)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Updated todo list to %s", encoded), nil
	})
}

func normalize(in []deep.TODO) ([]deep.TODO, error) {
	out := make([]deep.TODO, 0, len(in))
	inProgress := 0
	for i, item := range in {
		item.Content = strings.TrimSpace(item.Content)
		item.ActiveForm = strings.TrimSpace(item.ActiveForm)
		item.Status = strings.TrimSpace(item.Status)
		if item.Content == "" {
			return nil, fmt.Errorf("todos[%d] missing content", i)
		}
		switch item.Status {
		case "pending", "in_progress", "completed":
		default:
			return nil, fmt.Errorf("todos[%d] has invalid status %q", i, item.Status)
		}
		if item.Status == "in_progress" {
			inProgress++
		}
		out = append(out, item)
	}
	if inProgress > 1 {
		return nil, fmt.Errorf("only one todo can be in_progress")
	}
	return out, nil
}

func allCompleted(todos []deep.TODO) bool {
	if len(todos) == 0 {
		return false
	}
	for _, item := range todos {
		if item.Status != "completed" {
			return false
		}
	}
	return true
}

type middleware struct {
	*adk.BaseChatModelAgentMiddleware
	tool tool.BaseTool
}

func (m *middleware) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (context.Context, *adk.ChatModelAgentContext, error) {
	if runCtx == nil {
		runCtx = &adk.ChatModelAgentContext{}
	}
	next := *runCtx
	next.Instruction += prompt
	next.Tools = append(next.Tools, m.tool)
	return ctx, &next, nil
}
