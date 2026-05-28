package main

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// systemPrompt：s04 回到 s02 的措辞（权限逻辑现在由 hook 承担，不必写进提示词）。
func systemPrompt() string {
	return fmt.Sprintf("You are a coding agent at %s. Use tools to solve tasks. Act, don't explain.", workdir)
}

// agentLoop 结构与 s03 相同，但把硬编码的 check_permission 换成 hook：
//   - 工具执行前触发 PreToolUse（非空 = 阻止）
//   - 工具执行后触发 PostToolUse（观测）
//   - 循环退出前触发 Stop（非空 = 强制继续）
//
// loop 本身不再关心权限/日志/统计这些扩展逻辑——它们都在 hooks.go 里。
func agentLoop(ctx context.Context, agent model.ToolCallingChatModel, messages []*schema.Message) ([]*schema.Message, error) {
	dispatch := handlers()
	for {
		resp, err := agent.Generate(ctx, messages)
		if err != nil {
			return messages, err
		}
		messages = append(messages, resp)

		if len(resp.ToolCalls) == 0 {
			if force := triggerHooks(eventStop, &hookCtx{messages: messages}); force != "" {
				messages = append(messages, schema.UserMessage(force))
				continue
			}
			return messages, nil
		}

		for _, tc := range resp.ToolCalls {
			name := tc.Function.Name

			if blocked := triggerHooks(eventPreToolUse, &hookCtx{toolName: name, rawArgs: tc.Function.Arguments}); blocked != "" {
				messages = append(messages, schema.ToolMessage(blocked, tc.ID, schema.WithToolName(name)))
				continue
			}

			var output string
			if h, ok := dispatch[name]; ok {
				output = h(ctx, tc.Function.Arguments)
			} else {
				output = "Unknown: " + name
			}

			triggerHooks(eventPostToolUse, &hookCtx{toolName: name, rawArgs: tc.Function.Arguments, output: output})

			messages = append(messages, schema.ToolMessage(output, tc.ID, schema.WithToolName(name)))
		}
	}
}
