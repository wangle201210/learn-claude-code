package main

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// systemPrompt：s05 加入计划指引——多步任务先用 todo_write 规划，边做边更新状态。
func systemPrompt() string {
	return fmt.Sprintf("You are a coding agent at %s. "+
		"Before starting any multi-step task, use todo_write to plan your steps. "+
		"Update status as you go.", workdir)
}

// roundsSinceTodo 统计自上次 todo_write 以来过了几轮工具调用（跨多次 agentLoop 保持）。
var roundsSinceTodo = 0

// agentLoop 结构与 s04 相同，新增 nag reminder：连续 3 轮没更新 todo 就注入一条提醒。
func agentLoop(ctx context.Context, agent model.ToolCallingChatModel, messages []*schema.Message) ([]*schema.Message, error) {
	dispatch := handlers()
	for {
		// s05: nag —— 模型连续 3 轮没更新 todo，就提醒它。
		if roundsSinceTodo >= 3 && len(messages) > 0 {
			messages = append(messages, schema.UserMessage("<reminder>Update your todos.</reminder>"))
			roundsSinceTodo = 0
		}

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

		roundsSinceTodo++
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

			// s05: 调用了 todo_write 就重置计数。
			if name == "todo_write" {
				roundsSinceTodo = 0
			}

			messages = append(messages, schema.ToolMessage(output, tc.ID, schema.WithToolName(name)))
		}
	}
}
