package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/agentsmd"
	"github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/adk/middlewares/plantask"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/mcptools"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/memory"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/permission"
	agentruntime "github.com/wangle201210/learn-claude-code/final_eino_adk/internal/runtime"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/workspace"
)

// Build 装配最终版 agent：
//   - 模型层：传入 primary（必需）+ fallback（可为 nil）；
//   - Retry：框架内置（指数退避+jitter），默认 3 次；
//   - Failover：fallback 非 nil 时自动开启，在 429/529/overloaded 错误上切换。
//   - ADK middlewares：官方 patchtoolcalls / summarization / reduction /
//     filesystem / plantask / skill 覆盖前面章节的大部分 harness 能力。
//
// 它替代了前 19 章手写的 agent loop、工具分发、s11 错误恢复等。
func Build(ctx context.Context, primary, fallback model.ToolCallingChatModel, prompt permission.PromptFunc, record HistoryRecorder) (*adk.ChatModelAgent, *agentruntime.Runtime, error) {
	cwd, _ := os.Getwd()
	root := workspace.Dir()

	workspaceBackend, err := workspace.New(ctx, func(command string) error {
		if reason := permission.CheckDenyList(command); reason != "" {
			return errors.New(reason)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	patchMW, err := patchtoolcalls.New(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	compactState := &compactController{}
	summaryMW, err := summarization.New(ctx, &summarization.Config{
		Model: primary,
		Trigger: &summarization.TriggerCondition{
			ContextTokens:   50000,
			ContextMessages: 80,
		},
		TranscriptFilePath: filepath.Join(root, ".transcripts", "latest-summary-source.jsonl"),
	})
	if err != nil {
		return nil, nil, err
	}
	reductionMW, err := reduction.New(ctx, &reduction.Config{
		Backend:                   workspaceBackend,
		RootDir:                   filepath.Join(root, ".task_outputs", "tool-results"),
		MaxLengthForTrunc:         50000,
		MaxTokensForClear:         50000,
		ClearRetentionSuffixLimit: 6,
	})
	if err != nil {
		return nil, nil, err
	}
	filesystemMW, err := filesystem.New(ctx, &filesystem.MiddlewareConfig{
		Backend:           workspaceBackend,
		Shell:             workspaceBackend,
		UseMultiModalRead: false,
	})
	if err != nil {
		return nil, nil, err
	}
	taskMW, err := plantask.New(ctx, &plantask.Config{
		Backend: workspace.NewTaskBackend(workspaceBackend),
		BaseDir: filepath.Join(root, ".tasks"),
	})
	if err != nil {
		return nil, nil, err
	}
	handlers := []adk.ChatModelAgentMiddleware{
		patchMW,
		permission.New(prompt),
		newCompactMiddleware(compactState, summaryMW),
		summaryMW,
		reductionMW,
		filesystemMW,
		taskMW,
		memory.NewMiddleware(primary),
		newHistoryRecorderMiddleware(record),
	}

	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err == nil {
		agentsMW, err := agentsmd.New(ctx, &agentsmd.Config{
			Backend:             workspaceBackend,
			AgentsMDFiles:       []string{filepath.Join(root, "CLAUDE.md")},
			AllAgentsMDMaxBytes: 100000,
		})
		if err != nil {
			return nil, nil, err
		}
		handlers = append([]adk.ChatModelAgentMiddleware{handlers[0], agentsMW}, handlers[1:]...)
	}

	if _, err := os.Stat(filepath.Join(root, "skills")); err == nil {
		skillBackend, err := skill.NewBackendFromFilesystem(ctx, &skill.BackendFromFilesystemConfig{
			Backend: workspaceBackend,
			BaseDir: filepath.Join(root, "skills"),
		})
		if err != nil {
			return nil, nil, err
		}
		skillMW, err := skill.NewMiddleware(ctx, &skill.Config{
			Backend: skillBackend,
		})
		if err != nil {
			return nil, nil, err
		}
		handlers = append(handlers, skillMW)
	}

	runtimeState := agentruntime.New(root)
	runtimeState.Start(ctx)

	var extraTools []tool.BaseTool
	compactTool, err := buildCompactTool(compactState)
	if err != nil {
		return nil, nil, err
	}
	extraTools = append(extraTools, compactTool)

	agentTools, err := buildAgentTools(ctx, primary, workspaceBackend, prompt)
	if err != nil {
		return nil, nil, err
	}
	extraTools = append(extraTools, agentTools...)

	runtimeTools, err := runtimeState.BuildTools(ctx, workspaceBackend)
	if err != nil {
		return nil, nil, err
	}
	extraTools = append(extraTools, runtimeTools...)

	mcpTools, err := mcptools.Load(ctx)
	if err != nil {
		return nil, nil, err
	}
	extraTools = append(extraTools, mcpTools...)

	cfg := &adk.ChatModelAgentConfig{
		Name:        "FinalAgent",
		Description: "Coding agent (final version using eino abstractions).",
		Instruction: fmt.Sprintf(
			"You are a coding agent at %s. Use tools to solve tasks. Act, don't explain. "+
				"Respect workspace boundaries and ask before risky writes or commands. "+
				"Use task for isolated subtasks and teammate for bounded peer review or research. "+
				"Use background_execute for long-running commands and check_notifications/background_status for results. "+
				"Use schedule_cron/list_crons/cancel_cron for autonomous scheduled prompts. "+
				"Use send_message/check_inbox/request_plan/review_plan/request_shutdown for protocol coordination. "+
				"Use create_worktree/remove_worktree/keep_worktree when work should be isolated in a git worktree. "+
				"MCP tools, when configured through FINAL_EINO_MCP_* env vars, appear as normal tools.",
			cwd,
		),
		Model:         primary,
		MaxIterations: 25,
		Handlers:      handlers,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: extraTools,
			},
			EmitInternalEvents: true,
		},

		// s11 的 429/限流退避手写代码 ~100 行，被这一段配置取代。
		// BackoffFunc 为 nil 时框架用默认（指数退避+jitter，100ms→10s）。
		ModelRetryConfig: &adk.ModelRetryConfig{
			MaxRetries: 3,
		},
	}

	// s11 的 fallback 模型逻辑同样被一段配置取代。
	if fallback != nil {
		// 注意：ModelFailoverConfig 在 v0.9 没有非泛型别名（不像 ModelRetryConfig），
		// 需要显式实例化为 [*schema.Message]。
		cfg.ModelFailoverConfig = &adk.ModelFailoverConfig[*schema.Message]{
			MaxRetries: 1,
			ShouldFailover: func(_ context.Context, _ *schema.Message, err error) bool {
				if err == nil {
					return false
				}
				s := strings.ToLower(err.Error())
				return strings.Contains(s, "529") ||
					strings.Contains(s, "overloaded") ||
					strings.Contains(s, "rate limit") ||
					strings.Contains(s, "ratelimit")
			},
			GetFailoverModel: func(_ context.Context, _ *adk.FailoverContext[*schema.Message]) (
				model.BaseModel[*schema.Message], []*schema.Message, error,
			) {
				return fallback, nil, nil
			},
		}
	}

	agent, err := adk.NewChatModelAgent(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return agent, runtimeState, nil
}
