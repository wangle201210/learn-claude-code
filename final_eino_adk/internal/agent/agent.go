package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/agentsmd"
	"github.com/cloudwego/eino/adk/middlewares/dynamictool/toolsearch"
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
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/hooks"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/mcptools"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/memory"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/modelroute"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/permission"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/recovery"
	agentruntime "github.com/wangle201210/learn-claude-code/final_eino_adk/internal/runtime"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/todo"
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
func Build(ctx context.Context, primary, fallback model.ToolCallingChatModel, prompt permission.PromptFunc, record HistoryRecorder, compactState *CompactController) (*adk.ChatModelAgent, *agentruntime.Runtime, error) {
	cwd, _ := os.Getwd()
	root := workspace.Dir()
	if compactState == nil {
		compactState = NewCompactController()
	}

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
	summaryMW, err := summarization.New(ctx, &summarization.Config{
		Model: modelroute.WrapBase(primary, modelroute.Standard),
		Trigger: &summarization.TriggerCondition{
			ContextTokens:   50000,
			ContextMessages: 80,
		},
		EmitInternalEvents: true,
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
	todoMW, err := todo.New(nil)
	if err != nil {
		return nil, nil, err
	}
	var agentsMW adk.ChatModelAgentMiddleware
	agentsMDFiles, err := collectAgentInstructionFiles(root, cwd)
	if err != nil {
		return nil, nil, err
	}
	if len(agentsMDFiles) > 0 {
		agentsMW, err = agentsmd.New(ctx, &agentsmd.Config{
			Backend:             workspaceBackend,
			AgentsMDFiles:       agentsMDFiles,
			AllAgentsMDMaxBytes: 100000,
		})
		if err != nil {
			return nil, nil, err
		}
	}

	permissionMW, err := permission.NewFromRoot(root, prompt)
	if err != nil {
		return nil, nil, err
	}
	handlers := []adk.ChatModelAgentMiddleware{
		patchMW,
		permissionMW,
		newCompactMiddleware(compactState, summaryMW),
		summaryMW,
	}
	hooksMW, err := hooks.Load(root)
	if err != nil {
		return nil, nil, err
	}
	if hooksMW != nil {
		handlers = append(handlers, hooksMW)
	}
	if agentsMW != nil {
		// Eino agentsmd is transient model-call context. Keep it after summarization
		// so context compaction summarizes conversation state rather than CLAUDE.md.
		handlers = append(handlers, agentsMW)
	}
	handlers = append(handlers,
		reductionMW,
		filesystemMW,
		taskMW,
		todoMW,
		memory.NewMiddleware(modelroute.WrapBase(primary, modelroute.Standard)),
		newHistoryRecorderMiddleware(record),
	)

	skillBackend, err := buildSkillBackend(ctx, workspaceBackend, root)
	if err != nil {
		return nil, nil, err
	}
	if skillBackend != nil {
		skillMW, err := skill.NewMiddleware(ctx, &skill.Config{
			Backend:  skillBackend,
			AgentHub: newSkillAgentHub(primary, workspaceBackend, prompt),
			ModelHub: newSkillModelHub(primary),
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

	runtimeTools, err := runtimeState.BuildTools(ctx, primary, workspaceBackend, prompt)
	if err != nil {
		return nil, nil, err
	}
	extraTools = append(extraTools, runtimeTools...)

	mcpTools, err := mcptools.Load(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(mcpTools) > 0 {
		toolSearchMW, err := dynamicToolSearchMiddleware(ctx, mcpTools)
		if err != nil {
			return nil, nil, err
		}
		handlers = append(handlers, toolSearchMW)
	}

	instruction, err := buildInstruction(ctx, instructionInput{
		Workspace:    cwd,
		DirectTools:  extraTools,
		DynamicTools: mcpTools,
		SkillBackend: skillBackend,
	})
	if err != nil {
		return nil, nil, err
	}

	cfg := &adk.ChatModelAgentConfig{
		Name:          "FinalAgent",
		Description:   "Coding agent (final version using eino abstractions).",
		Instruction:   instruction,
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
			ShouldRetry: func(_ context.Context, retryCtx *adk.RetryContext) *adk.RetryDecision {
				if retryCtx == nil {
					return &adk.RetryDecision{}
				}
				if retryCtx.Err != nil {
					return &adk.RetryDecision{Retry: recovery.IsRetryableModelError(retryCtx.Err)}
				}
				if recovery.IsOutputTruncated(retryCtx.OutputMessage) {
					next := append(cloneMessages(retryCtx.InputMessages), cloneMessage(retryCtx.OutputMessage))
					next = append(next, recovery.ContinuationMessage())
					return &adk.RetryDecision{
						Retry:                        true,
						ModifiedInputMessages:        next,
						PersistModifiedInputMessages: true,
						RejectReason:                 "model output hit max_tokens",
					}
				}
				return &adk.RetryDecision{}
			},
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

func dynamicToolSearchMiddleware(ctx context.Context, dynamicTools []tool.BaseTool) (adk.ChatModelAgentMiddleware, error) {
	if len(dynamicTools) == 0 {
		return nil, nil
	}
	return toolsearch.New(ctx, &toolsearch.Config{
		DynamicTools: dynamicTools,
	})
}
