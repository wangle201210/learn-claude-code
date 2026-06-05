package runtime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/adk/middlewares/plantask"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/modelroute"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/permission"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/workspace"
)

const (
	teammateIdlePollInterval   = 200 * time.Millisecond
	defaultTeammateIdleTimeout = 2 * time.Second
	teammateMaxAutoTasks       = 3
)

type teammateRun struct {
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Prompt    string    `json:"prompt"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
}

type spawnTeammateArgs struct {
	Name   string `json:"name" jsonschema:"required" jsonschema_description:"Unique teammate identifier"`
	Role   string `json:"role,omitempty" jsonschema_description:"Short role description, such as reviewer or researcher"`
	Prompt string `json:"prompt" jsonschema:"required" jsonschema_description:"Initial task for the teammate"`
}

func (r *Runtime) spawnTeammate(ctx context.Context, primary model.ToolCallingChatModel, workspaceBackend *workspace.Backend, prompt permission.PromptFunc, input *spawnTeammateArgs) (string, error) {
	name, err := validateMailboxName(input.Name)
	if err != nil {
		return "", err
	}
	taskPrompt := strings.TrimSpace(input.Prompt)
	if taskPrompt == "" {
		return "", errors.New("prompt is required")
	}
	role := strings.TrimSpace(input.Role)
	if role == "" {
		role = "teammate"
	}
	if primary == nil {
		return "", errors.New("primary model is required")
	}

	r.mu.Lock()
	if run, ok := r.teammates[name]; ok && run.Status == "running" {
		r.mu.Unlock()
		return fmt.Sprintf("Teammate %q is already running.", name), nil
	}
	r.teammates[name] = &teammateRun{
		Name:      name,
		Role:      role,
		Prompt:    taskPrompt,
		Status:    "running",
		StartedAt: time.Now(),
	}
	r.mu.Unlock()

	go r.runTeammate(ctx, primary, workspaceBackend, prompt, name, role, taskPrompt)
	return fmt.Sprintf("Spawned teammate %q as %s. Results will arrive through notifications.", name, role), nil
}

func (r *Runtime) runTeammate(_ context.Context, primary model.ToolCallingChatModel, workspaceBackend *workspace.Backend, prompt permission.PromptFunc, name, role, taskPrompt string) {
	baseCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	ctx, routeUsage := modelroute.WithUsage(baseCtx)

	result, err := r.runTeammateLifecycle(ctx, primary, workspaceBackend, prompt, name, role, taskPrompt)
	status := "succeeded"
	body := result
	if err != nil {
		status = "failed"
		body = err.Error()
	}
	if strings.TrimSpace(body) == "" {
		body = "Teammate completed without a text result."
	}
	if summary := modelroute.FormatUsage(routeUsage); summary != "" {
		body = strings.TrimSpace(body) + "\n\n" + summary
	}

	r.mu.Lock()
	if run, ok := r.teammates[name]; ok {
		run.Status = status
	}
	r.mu.Unlock()

	_ = r.appendMessage(mailboxMessage{
		ID:      fmt.Sprintf("teammate-%d", time.Now().UnixNano()),
		Time:    time.Now().Format(time.RFC3339),
		From:    name,
		To:      "main",
		Kind:    "teammate_result",
		Subject: status,
		Body:    body,
	})
}

func (r *Runtime) runTeammateLifecycle(ctx context.Context, primary model.ToolCallingChatModel, workspaceBackend *workspace.Backend, prompt permission.PromptFunc, name, role, initialPrompt string) (string, error) {
	var parts []string
	result, err := runTeammateAgent(ctx, primary, workspaceBackend, prompt, name, role, initialPrompt)
	if strings.TrimSpace(result) != "" {
		parts = append(parts, result)
	}
	if err != nil {
		return strings.Join(parts, "\n\n"), err
	}

	for i := 0; i < teammateMaxAutoTasks; i++ {
		task, ok, err := r.waitAndClaimBoardTask(ctx, name, r.teammateIdle)
		if err != nil {
			return strings.Join(parts, "\n\n"), err
		}
		if !ok {
			break
		}
		autoPrompt := fmt.Sprintf(
			"<auto-claimed task_id=%q>\nSubject: %s\nDescription: %s\n\nUse TaskGet and TaskUpdate if useful. Complete the task or report why it remains blocked.\n</auto-claimed>",
			task.ID,
			task.Subject,
			task.Description,
		)
		taskResult, err := runTeammateAgent(ctx, primary, workspaceBackend, prompt, name, role, autoPrompt)
		if strings.TrimSpace(taskResult) != "" {
			parts = append(parts, fmt.Sprintf("Auto-claimed task #%s:\n%s", task.ID, taskResult))
		}
		if err != nil {
			return strings.Join(parts, "\n\n"), err
		}
	}

	return strings.Join(parts, "\n\n"), nil
}

func (r *Runtime) waitAndClaimBoardTask(ctx context.Context, owner string, timeout time.Duration) (*boardTask, bool, error) {
	if timeout <= 0 {
		r.taskMu.Lock()
		task, ok, err := claimNextBoardTask(r.workdir, owner)
		r.taskMu.Unlock()
		return task, ok, err
	}
	deadline := time.Now().Add(timeout)
	for {
		r.taskMu.Lock()
		task, ok, err := claimNextBoardTask(r.workdir, owner)
		r.taskMu.Unlock()
		if err != nil || ok {
			return task, ok, err
		}
		if time.Now().After(deadline) {
			return nil, false, nil
		}
		sleep := teammateIdlePollInterval
		if remaining := time.Until(deadline); remaining < sleep {
			sleep = remaining
		}
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-time.After(sleep):
		}
	}
}

func runTeammateAgent(ctx context.Context, primary model.ToolCallingChatModel, workspaceBackend *workspace.Backend, prompt permission.PromptFunc, name, role, taskPrompt string) (string, error) {
	patchMW, err := patchtoolcalls.New(ctx, nil)
	if err != nil {
		return "", err
	}
	reductionMW, err := reduction.New(ctx, &reduction.Config{
		Backend:                   workspaceBackend,
		RootDir:                   filepath.Join(workspace.Dir(), ".task_outputs", "teammates", name),
		MaxLengthForTrunc:         50000,
		MaxTokensForClear:         50000,
		ClearRetentionSuffixLimit: 6,
	})
	if err != nil {
		return "", err
	}
	filesystemMW, err := filesystem.New(ctx, &filesystem.MiddlewareConfig{
		Backend:           workspaceBackend,
		Shell:             workspaceBackend,
		UseMultiModalRead: false,
	})
	if err != nil {
		return "", err
	}
	taskMW, err := plantask.New(ctx, &plantask.Config{
		Backend: workspace.NewTaskBackend(workspaceBackend),
		BaseDir: filepath.Join(workspace.Dir(), ".tasks"),
	})
	if err != nil {
		return "", err
	}
	permissionMW, err := permission.NewFromRoot(workspace.Dir(), prompt)
	if err != nil {
		return "", err
	}

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "teammate_" + name,
		Description: "Background teammate agent.",
		Instruction: fmt.Sprintf(
			"You are %q, a background %s. Complete the delegated task using workspace tools. "+
				"Use TaskList/TaskGet/TaskUpdate when working from the shared task board. After the initial task, you may be given auto-claimed Task* work. "+
				"Return a concise final result with evidence, changed files, and remaining risks. Do not spawn teammates.",
			name,
			role,
		),
		Model:         primary,
		MaxIterations: 12,
		Handlers: []adk.ChatModelAgentMiddleware{
			patchMW,
			permissionMW,
			reductionMW,
			filesystemMW,
			taskMW,
		},
		ModelRetryConfig: &adk.ModelRetryConfig{
			MaxRetries: 3,
		},
	})
	if err != nil {
		return "", err
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	iter := runner.Run(ctx, []adk.Message{schema.UserMessage(taskPrompt)})
	var lastText string
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return lastText, event.Err
		}
		msg, _, err := adk.GetMessage(event)
		if err != nil {
			return lastText, err
		}
		if msg != nil && msg.Role == schema.Assistant && strings.TrimSpace(msg.Content) != "" {
			lastText = msg.Content
		}
	}
	return lastText, nil
}
