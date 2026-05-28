package main

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// systemPrompt：s01 是 "Use bash"，s02 改成 "Use tools"（现在有 5 个工具）。
func systemPrompt() string {
	return fmt.Sprintf("You are a coding agent at %s. Use tools to solve tasks. Act, don't explain.", workdir)
}

// agentLoop 的结构与 s01 完全一致；唯一的变化是工具执行那段：
// 从硬编码 runBash 改为按工具名查表分发（dispatch）。
func agentLoop(ctx context.Context, agent model.ToolCallingChatModel, messages []*schema.Message) ([]*schema.Message, error) {
	dispatch := handlers()
	for {
		resp, err := agent.Generate(ctx, messages)
		if err != nil {
			return messages, err
		}
		messages = append(messages, resp)

		if len(resp.ToolCalls) == 0 {
			return messages, nil
		}

		for _, tc := range resp.ToolCalls {
			name := tc.Function.Name
			fmt.Printf("\033[33m> %s\033[0m\n", name)

			var output string
			if h, ok := dispatch[name]; ok {
				output = h(ctx, tc.Function.Arguments)
			} else {
				output = "Unknown: " + name
			}
			fmt.Println(truncate(output, 200))

			messages = append(messages, schema.ToolMessage(output, tc.ID, schema.WithToolName(name)))
		}
	}
}
