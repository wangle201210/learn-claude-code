package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// systemPrompt 告诉模型它是一个 coding agent，用 bash 解决问题、直接动手别解释。
func systemPrompt() string {
	cwd, _ := os.Getwd()
	return fmt.Sprintf("You are a coding agent at %s. Use bash to solve tasks. Act, don't explain.", cwd)
}

// agentLoop 是整个 agent 的内核：
//
//	for {
//	    生成 -> 没调工具就结束 -> 调了就执行、把结果喂回去 -> 继续
//	}
//
// 返回追加了本轮全部消息的对话历史。
func agentLoop(ctx context.Context, agent model.ToolCallingChatModel, messages []*schema.Message) ([]*schema.Message, error) {
	for {
		resp, err := agent.Generate(ctx, messages)
		if err != nil {
			return messages, err
		}
		messages = append(messages, resp)

		// 模型没有调用工具 —— 说明它认为任务完成了，退出循环。
		if len(resp.ToolCalls) == 0 {
			return messages, nil
		}

		// 逐个执行工具调用，把结果作为 ToolMessage 追加回去。
		for _, tc := range resp.ToolCalls {
			var args struct {
				Command string `json:"command"`
			}
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)

			fmt.Printf("\033[33m$ %s\033[0m\n", args.Command)
			output := runBash(ctx, args.Command)
			fmt.Println(truncate(output, 200))

			messages = append(messages, schema.ToolMessage(output, tc.ID, schema.WithToolName(tc.Function.Name)))
		}
	}
}
