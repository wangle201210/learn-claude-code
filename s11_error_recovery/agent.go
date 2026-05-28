package main

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// roundsSinceTodo 统计自上次 todo_write 以来过了几轮工具调用（跨多次 agentLoop 保持）。
var roundsSinceTodo = 0

// agentLoop 在 s07 基础上接入 s08 的压缩管线：每轮 Generate 前先跑三层廉价
// 压缩（budget→snip→micro），仍超阈值再做 L4 LLM 摘要；Generate 因 prompt
// 过长失败时，做反应式压缩并重试（最多一次）。其余（nag、hooks）沿用 s07。
func agentLoop(ctx context.Context, agent model.ToolCallingChatModel, messages []*schema.Message) ([]*schema.Message, error) {
	dispatch := handlers()

	// s11: 错误恢复状态 + 可变 max_tokens（输出截断时会升级）。
	state := &recoveryState{}
	maxTokens := defaultMaxTokens

	// s09: 本次会话开始时挑一次相关记忆，注入到 SYSTEM（Eino 适配：放 system 而非
	// 改写 user 消息，避免破坏 tool_call/tool 配对）。
	memories := loadMemories(ctx, messages)

	for {
		// s05: nag —— 模型连续 3 轮没更新 todo，就提醒它。
		if roundsSinceTodo >= 3 && len(messages) > 0 {
			messages = append(messages, schema.UserMessage("<reminder>Update your todos.</reminder>"))
			roundsSinceTodo = 0
		}

		// s10: 每轮按真实状态分段组装 system（带进程内缓存），再拼上 s09 的相关记忆。
		if len(messages) > 0 && messages[0].Role == schema.System {
			sys := getSystemPrompt(buildContext())
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

		// s11: 把 Generate 包进错误恢复。generateWithRetry 处理 429/529 退避与
		// fallback 模型；返回的错误交给下面的恢复路径。
		resp, err := generateWithRetry(ctx, agent, messages, maxTokens, state)
		if err != nil {
			// Path 2: prompt 过长 → 反应式压缩（仅一次），仍不行则不可恢复退出。
			if isPromptTooLong(err) {
				if !state.hasAttemptedReactiveCompact {
					fmt.Println("  \033[31m[reactive compact]\033[0m")
					messages = reactiveCompact(ctx, messages)
					state.hasAttemptedReactiveCompact = true
					continue
				}
				fmt.Println("  \033[31m[unrecoverable] still too long after compact\033[0m")
				messages = append(messages, schema.AssistantMessage("[Error] Context too large, cannot continue.", nil))
				return messages, nil
			}
			// 其它错误：不可恢复，记一条错误消息后退出。
			fmt.Printf("  \033[31m[unrecoverable] %s\033[0m\n", truncate(err.Error(), 100))
			messages = append(messages, schema.AssistantMessage("[Error] "+truncate(err.Error(), 200), nil))
			return messages, nil
		}

		// Path 1: 输出截断（FinishReason==length）→ 先升级 max_tokens，再 continuation。
		if resp.ResponseMeta != nil && (resp.ResponseMeta.FinishReason == "length" || resp.ResponseMeta.FinishReason == "max_tokens") {
			if !state.hasEscalated {
				maxTokens = escalatedMaxTokens
				state.hasEscalated = true
				fmt.Printf("  \033[33m[max_tokens] escalating %d -> %d\033[0m\n", defaultMaxTokens, escalatedMaxTokens)
				continue
			}
			messages = append(messages, resp)
			if state.recoveryCount < maxRecoveryRetries {
				messages = append(messages, schema.UserMessage(continuationPrompt))
				state.recoveryCount++
				fmt.Printf("  \033[33m[max_tokens] continuation %d/%d\033[0m\n", state.recoveryCount, maxRecoveryRetries)
				continue
			}
			fmt.Println("  \033[31m[max_tokens] recovery limit reached\033[0m")
			return messages, nil
		}

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
