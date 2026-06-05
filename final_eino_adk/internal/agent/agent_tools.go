package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/permission"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/workspace"
)

type delegateArgs struct {
	Request string `json:"request" jsonschema:"required" jsonschema_description:"The task or question for the delegated agent"`
}

func buildAgentTools(ctx context.Context, primary model.ToolCallingChatModel, workspaceBackend *workspace.Backend, prompt permission.PromptFunc) ([]tool.BaseTool, error) {
	taskAgent, err := buildDelegateAgent(ctx, "task_subagent",
		"Run an isolated coding/research subtask with workspace tools and return the result.",
		"You are a focused subagent. Complete the delegated task using available workspace tools. Return concise findings, changed files, and any remaining risks. Do not delegate further.",
		primary, nil, workspaceBackend, prompt)
	if err != nil {
		return nil, err
	}
	taskTool, err := wrapAgentTool(ctx, "task", "Launch an isolated subagent for focused subtasks.", taskAgent)
	if err != nil {
		return nil, err
	}

	teammateAgent, err := buildDelegateAgent(ctx, "teammate",
		"Ask a teammate agent to review, research, or implement a bounded piece of work.",
		"You are a teammate agent. Use the chat history and workspace tools to help with the requested role. Be direct: report conclusions, evidence, and concrete next steps. Do not delegate further.",
		primary, nil, workspaceBackend, prompt)
	if err != nil {
		return nil, err
	}
	teammateTool, err := wrapAgentTool(ctx, "teammate", "Ask a teammate agent for review, research, or implementation help.", teammateAgent)
	if err != nil {
		return nil, err
	}

	return []tool.BaseTool{taskTool, teammateTool}, nil
}

func buildDelegateAgent(ctx context.Context, name, desc, instruction string, primary model.ToolCallingChatModel, modelOverride model.BaseChatModel, workspaceBackend *workspace.Backend, prompt permission.PromptFunc) (*adk.ChatModelAgent, error) {
	patchMW, err := patchtoolcalls.New(ctx, nil)
	if err != nil {
		return nil, err
	}
	reductionMW, err := reduction.New(ctx, &reduction.Config{
		Backend:                   workspaceBackend,
		RootDir:                   filepath.Join(workspace.Dir(), ".task_outputs", name),
		MaxLengthForTrunc:         50000,
		MaxTokensForClear:         50000,
		ClearRetentionSuffixLimit: 6,
	})
	if err != nil {
		return nil, err
	}
	filesystemMW, err := filesystem.New(ctx, &filesystem.MiddlewareConfig{
		Backend:           workspaceBackend,
		Shell:             workspaceBackend,
		UseMultiModalRead: false,
	})
	if err != nil {
		return nil, err
	}
	permissionMW, err := permission.NewFromRoot(workspace.Dir(), prompt)
	if err != nil {
		return nil, err
	}

	agentModel := model.BaseChatModel(primary)
	if modelOverride != nil {
		agentModel = modelOverride
	}

	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          name,
		Description:   desc,
		Instruction:   instruction,
		Model:         agentModel,
		MaxIterations: 12,
		Handlers: []adk.ChatModelAgentMiddleware{
			patchMW,
			permissionMW,
			reductionMW,
			filesystemMW,
		},
		ModelRetryConfig: &adk.ModelRetryConfig{
			MaxRetries: 3,
		},
	})
}

func wrapAgentTool(ctx context.Context, name, desc string, agent adk.Agent) (tool.BaseTool, error) {
	wrapped := adk.NewAgentTool(ctx, agent)
	invokable, ok := wrapped.(tool.InvokableTool)
	if !ok {
		return nil, fmt.Errorf("agent tool %s is not invokable", name)
	}

	return utils.InferTool[*delegateArgs, string](name, desc, func(ctx context.Context, input *delegateArgs) (string, error) {
		request := input.Request
		if request == "" {
			return "", fmt.Errorf("request is required")
		}
		payload, err := json.Marshal(map[string]string{"request": request})
		if err != nil {
			return "", err
		}
		return invokable.InvokableRun(ctx, string(payload))
	})
}
