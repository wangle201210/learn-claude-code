package main

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// systemPrompt：s07 注入技能目录（第一层，廉价常驻）。
func systemPrompt() string {
	return buildSystem()
}

// roundsSinceTodo 统计自上次 todo_write 以来过了几轮工具调用（跨多次 agentLoop 保持）。
var roundsSinceTodo = 0

// agentLoop 在 s07 基础上接入 s08 的压缩管线：每轮 Generate 前先跑三层廉价
// 压缩（budget→snip→micro），仍超阈值再做 L4 LLM 摘要；Generate 因 prompt
// 过长失败时，做反应式压缩并重试（最多一次）。其余（nag、hooks）沿用 s07。
func agentLoop(ctx context.Context, agent model.ToolCallingChatModel, messages []*schema.Message) ([]*schema.Message, error) {
	dispatch := handlers()
	reactiveRetries := 0

	// s09: 本次会话开始时挑一次相关记忆，注入到 SYSTEM（Eino 适配：放 system 而非
	// 改写 user 消息，避免破坏 tool_call/tool 配对）。
	memories := loadMemories(ctx, messages)

	for {
		// s05: nag —— 模型连续 3 轮没更新 todo，就提醒它。
		if roundsSinceTodo >= 3 && len(messages) > 0 {
			messages = append(messages, schema.UserMessage("<reminder>Update your todos.</reminder>"))
			roundsSinceTodo = 0
		}

		// s09: 每轮用最新记忆索引重建 system，并拼上本次相关记忆内容。
		if len(messages) > 0 && messages[0].Role == schema.System {
			sys := systemPrompt()
			if memories != "" {
				sys += "\n\n" + memories
			}
			messages[0] = schema.SystemMessage(sys)
		}

		// s09: 压缩前拍快照（复制 Role+Content），用于结束后无损提取记忆。
		preCompress := make([]*schema.Message, len(messages))
		for i, m := range messages {
			preCompress[i] = &schema.Message{Role: m.Role, Content: m.Content}
		}

		// s08: 三层廉价预处理（0 次 API），顺序对齐 CC 源码：budget → snip → micro。
		messages = toolResultBudget(messages) // L3：大结果落盘
		messages = snipCompact(messages)      // L1：裁中间
		messages = microCompact(messages)     // L2：旧结果占位符
		// L4：仍超阈值就做一次 LLM 摘要。
		if estimateSize(messages) > contextLimit {
			fmt.Println("[auto compact]")
			messages = compactHistory(ctx, messages)
		}
		// 出栏前最后一道：保证 tool_call/tool_output 配对完整。
		messages = enforcePairing(messages)

		resp, err := agent.Generate(ctx, messages)
		if err != nil {
			// s08: prompt 过长时反应式压缩并重试（最多 maxReactiveRetries 次）。
			if reactiveRetries < maxReactiveRetries && isPromptTooLong(err) {
				fmt.Println("[reactive compact]")
				messages = reactiveCompact(ctx, messages)
				reactiveRetries++
				continue
			}
			return messages, err
		}
		reactiveRetries = 0
		messages = append(messages, resp)

		if len(resp.ToolCalls) == 0 {
			if force := triggerHooks(eventStop, &hookCtx{messages: messages}); force != "" {
				messages = append(messages, schema.UserMessage(force))
				continue
			}
			// s09: 用压缩前快照提取新记忆，并在记忆过多时整合。
			extractMemories(ctx, preCompress)
			consolidateMemories(ctx)
			return messages, nil
		}

		roundsSinceTodo++
		compacted := false
		for _, tc := range resp.ToolCalls {
			name := tc.Function.Name

			// s08: compact 工具触发整段历史压缩。compactHistory 会替换整个
			// messages（含这条带 compact 调用的 assistant），所以不会留下孤立的
			// tool 消息；break 后用压缩好的上下文进入下一轮。
			if name == "compact" {
				messages = compactHistory(ctx, messages)
				compacted = true
				break
			}

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
		_ = compacted // 压缩与否都继续下一轮循环
	}
}
