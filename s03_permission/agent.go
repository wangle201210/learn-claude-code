package main

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// systemPrompt：s03 强调破坏性操作需要用户批准。
func systemPrompt() string {
	return fmt.Sprintf("You are a coding agent at %s. All destructive operations require user approval.", workdir)
}

// agentLoop 与 s02 相同，只在工具执行前插入了一行 checkPermission。
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
			fmt.Printf("\033[36m> %s\033[0m\n", name)

			// s03 新增：执行前过一遍三道权限闸门。
			if !checkPermission(name, tc.Function.Arguments) {
				messages = append(messages, schema.ToolMessage("Permission denied.", tc.ID, schema.WithToolName(name)))
				continue
			}

			var output string
			if h, ok := dispatch[name]; ok {
				output = h(ctx, tc.Function.Arguments)
			} else {
				output = "Unknown: " + name
			}
			fmt.Println(truncate(output, 2000))

			messages = append(messages, schema.ToolMessage(output, tc.ID, schema.WithToolName(name)))
		}
	}
}
