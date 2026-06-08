package main

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/schema"
	"github.com/wangle201210/learn-claude-code/internal/cliexit"
)

func main() {
	ctx := context.Background()

	base, err := NewModel(ctx)
	if err != nil {
		cliexit.ExitWithError(err)
	}
	agent, err := base.WithTools(toolInfos())
	if err != nil {
		panic(err)
	}
	// 子 agent 用同一个底座模型，但只绑定子工具集（无 task，防递归）。
	subAgent, err = base.WithTools(subToolInfos())
	if err != nil {
		panic(err)
	}
	// summarizer 用无工具的底座模型，专门给 L4/反应式压缩生成摘要。
	summarizer = base

	fmt.Println("s09: Memory — 跨会话持久化知识")
	fmt.Println("输入问题，回车发送。输入 q 退出。")
	fmt.Println()

	messages := []*schema.Message{schema.SystemMessage(systemPrompt())}

	for {
		query, ok := readLine("\033[36ms09 >> \033[0m")
		if !ok {
			break
		}
		if query == "" || query == "q" || query == "exit" {
			break
		}

		// UserPromptSubmit：用户输入到达 LLM 之前先过一遍 hook。
		triggerHooks(eventUserPromptSubmit, &hookCtx{query: query})

		messages = append(messages, schema.UserMessage(query))
		messages, err = agentLoop(ctx, agent, messages)
		if err != nil {
			fmt.Printf("\033[31merror: %v\033[0m\n", err)
			continue
		}

		fmt.Println(messages[len(messages)-1].Content)
		fmt.Println()
	}
}
