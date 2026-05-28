package main

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/schema"
)

func main() {
	ctx := context.Background()

	base, err := NewModel(ctx)
	if err != nil {
		panic(err)
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

	fmt.Println("s06: Subagent — 用全新上下文开子 agent，只回传摘要")
	fmt.Println("输入问题，回车发送。输入 q 退出。")
	fmt.Println()

	messages := []*schema.Message{schema.SystemMessage(systemPrompt())}

	for {
		query, ok := readLine("\033[36ms06 >> \033[0m")
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
