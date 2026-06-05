package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// BuildAgent 装配最终版 agent：
//   - 模型层：传入 primary（必需）+ fallback（可为 nil）；
//   - Retry：框架内置（指数退避+jitter），默认 3 次；
//   - Failover：fallback 非 nil 时自动开启，在 429/529/overloaded 错误上切换。
//
// 它替代了前 19 章手写的 agent loop、工具分发、s11 错误恢复等。
func BuildAgent(ctx context.Context, primary, fallback model.ToolCallingChatModel) (*adk.ChatModelAgent, error) {
	cwd, _ := os.Getwd()

	// 4 个工具用 utils.InferTool 从 Go 函数自动推 schema：手写 ParamsOneOf 的活儿消失了。
	tools := []tool.BaseTool{
		newBashTool(),
		newReadTool(),
		newWriteTool(),
		newGlobTool(),
	}
	// 接真实 MCP server（例如 stdio 启动的 filesystem/fetch server），把工具并入：
	//   import "github.com/cloudwego/eino-ext/components/tool/mcp"
	//   import mcpcli "github.com/mark3labs/mcp-go/client"
	//   cli, _ := mcpcli.NewStdioMCPClient("npx", nil, "-y", "@modelcontextprotocol/server-filesystem", workdir)
	//   mcpTools, _ := mcp.GetTools(ctx, &mcp.Config{Cli: cli})
	//   tools = append(tools, mcpTools...)

	cfg := &adk.ChatModelAgentConfig{
		Name:        "FinalAgent",
		Description: "Coding agent (final version using eino abstractions).",
		Instruction: fmt.Sprintf(
			"You are a coding agent at %s. Use tools to solve tasks. Act, don't explain.",
			cwd,
		),
		Model: primary,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools},
		},
		MaxIterations: 20,

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
