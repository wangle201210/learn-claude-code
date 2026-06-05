package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino-ext/adk/backend/local"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/agentsmd"
	"github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/adk/middlewares/plantask"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// BuildAgent 装配最终版 agent：
//   - 模型层：传入 primary（必需）+ fallback（可为 nil）；
//   - Retry：框架内置（指数退避+jitter），默认 3 次；
//   - Failover：fallback 非 nil 时自动开启，在 429/529/overloaded 错误上切换。
//   - ADK middlewares：官方 patchtoolcalls / summarization / reduction /
//     filesystem / plantask / skill 覆盖前面章节的大部分 harness 能力。
//
// 它替代了前 19 章手写的 agent loop、工具分发、s11 错误恢复等。
func BuildAgent(ctx context.Context, primary, fallback model.ToolCallingChatModel) (*adk.ChatModelAgent, error) {
	cwd, _ := os.Getwd()

	localBackend, err := local.NewBackend(ctx, &local.Config{
		ValidateCommand: func(command string) error {
			if reason := checkDenyList(command); reason != "" {
				return errors.New(reason)
			}
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	workspace := &workspaceBackend{Backend: localBackend, shell: localBackend}

	patchMW, err := patchtoolcalls.New(ctx, nil)
	if err != nil {
		return nil, err
	}
	summaryMW, err := summarization.New(ctx, &summarization.Config{
		Model: primary,
		Trigger: &summarization.TriggerCondition{
			ContextTokens:   50000,
			ContextMessages: 80,
		},
		TranscriptFilePath: filepath.Join(workdir, ".transcripts", "latest-summary-source.jsonl"),
	})
	if err != nil {
		return nil, err
	}
	reductionMW, err := reduction.New(ctx, &reduction.Config{
		Backend:                   workspace,
		RootDir:                   filepath.Join(workdir, ".task_outputs", "tool-results"),
		MaxLengthForTrunc:         50000,
		MaxTokensForClear:         50000,
		ClearRetentionSuffixLimit: 6,
	})
	if err != nil {
		return nil, err
	}
	filesystemMW, err := filesystem.New(ctx, &filesystem.MiddlewareConfig{
		Backend:           workspace,
		Shell:             workspace,
		UseMultiModalRead: false,
	})
	if err != nil {
		return nil, err
	}
	taskMW, err := plantask.New(ctx, &plantask.Config{
		Backend: &taskBackend{backend: workspace},
		BaseDir: filepath.Join(workdir, ".tasks"),
	})
	if err != nil {
		return nil, err
	}
	handlers := []adk.ChatModelAgentMiddleware{
		patchMW,
		newPermissionMiddleware(),
		summaryMW,
		reductionMW,
		filesystemMW,
		taskMW,
		newMemoryMiddleware(primary),
	}

	if _, err := os.Stat(filepath.Join(workdir, "CLAUDE.md")); err == nil {
		agentsMW, err := agentsmd.New(ctx, &agentsmd.Config{
			Backend:             workspace,
			AgentsMDFiles:       []string{filepath.Join(workdir, "CLAUDE.md")},
			AllAgentsMDMaxBytes: 100000,
		})
		if err != nil {
			return nil, err
		}
		handlers = append([]adk.ChatModelAgentMiddleware{handlers[0], agentsMW}, handlers[1:]...)
	}

	if _, err := os.Stat(filepath.Join(workdir, "skills")); err == nil {
		skillBackend, err := skill.NewBackendFromFilesystem(ctx, &skill.BackendFromFilesystemConfig{
			Backend: workspace,
			BaseDir: filepath.Join(workdir, "skills"),
		})
		if err != nil {
			return nil, err
		}
		skillMW, err := skill.NewMiddleware(ctx, &skill.Config{
			Backend: skillBackend,
		})
		if err != nil {
			return nil, err
		}
		handlers = append(handlers, skillMW)
	}

	cfg := &adk.ChatModelAgentConfig{
		Name:        "FinalAgent",
		Description: "Coding agent (final version using eino abstractions).",
		Instruction: fmt.Sprintf(
			"You are a coding agent at %s. Use tools to solve tasks. Act, don't explain. "+
				"Respect workspace boundaries and ask before risky writes or commands.",
			cwd,
		),
		Model:         primary,
		MaxIterations: 20,
		Handlers:      handlers,

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

	return adk.NewChatModelAgent(ctx, cfg)
}
