package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// subAgent 是绑定了子工具集（无 task）的模型实例，在 main 里初始化。
var subAgent model.ToolCallingChatModel

// subSystemPrompt：子 agent 自己的系统提示——完成任务后给出简洁总结，不再往下委派。
func subSystemPrompt() string {
	return fmt.Sprintf("You are a coding agent at %s. "+
		"Complete the task you were given, then return a concise summary. "+
		"Do not delegate further.", workdir)
}

// runTask 是 task 工具的 handler：解析 description 后启动一个子 agent。
func runTask(ctx context.Context, args string) string {
	var a struct {
		Description string `json:"description"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	return spawnSubagent(ctx, a.Description)
}

// spawnSubagent 用全新的 messages[] 跑一个独立的内层循环（上下文隔离），
// 最多 30 轮，只把最终文本摘要返回给父 agent——中间过程全部丢弃。
func spawnSubagent(ctx context.Context, description string) string {
	fmt.Println("\n\033[35m[Subagent spawned]\033[0m")

	// fresh context：子 agent 看不到父 agent 的对话历史。
	messages := []*schema.Message{
		schema.SystemMessage(subSystemPrompt()),
		schema.UserMessage(description),
	}
	dispatch := subHandlers()

	for i := 0; i < 30; i++ { // 安全上限：最多 30 轮
		resp, err := subAgent.Generate(ctx, messages)
		if err != nil {
			return "Subagent error: " + err.Error()
		}
		messages = append(messages, resp)

		if len(resp.ToolCalls) == 0 {
			break
		}

		for _, tc := range resp.ToolCalls {
			name := tc.Function.Name

			// 子 agent 的工具调用同样要过 hooks（权限照样生效）。
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
			fmt.Printf("  \033[90m[sub] %s: %s\033[0m\n", name, truncate(output, 100))

			messages = append(messages, schema.ToolMessage(output, tc.ID, schema.WithToolName(name)))
		}
	}

	result := extractText(messages)
	fmt.Println("\033[35m[Subagent done]\033[0m")
	return result
}

// extractText 从后往前找最后一条非空的 assistant 文本作为摘要；
// 若 30 轮用尽时还停在 tool 调用上、没有最终文本，则给出兜底说明。
func extractText(messages []*schema.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == schema.Assistant && messages[i].Content != "" {
			return messages[i].Content
		}
	}
	return "Subagent stopped after 30 turns without final answer."
}
