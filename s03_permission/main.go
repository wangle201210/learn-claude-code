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

	fmt.Println("s03: Permission")
	fmt.Println("输入问题，回车发送。输入 q 退出。")
	fmt.Println()

	messages := []*schema.Message{schema.SystemMessage(systemPrompt())}

	for {
		// 复用 permission.go 里的共享 stdin reader，与工具审批同一个输入流。
		query, ok := readLine("\033[36ms03 >> \033[0m")
		if !ok {
			break
		}
		if query == "" || query == "q" || query == "exit" {
			break
		}

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
