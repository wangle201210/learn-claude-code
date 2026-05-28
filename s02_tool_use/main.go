package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cloudwego/eino/schema"
)

func main() {
	ctx := context.Background()

	base, err := NewModel(ctx)
	if err != nil {
		panic(err)
	}
	// 把全部 5 个工具绑定到模型。
	agent, err := base.WithTools(toolInfos())
	if err != nil {
		panic(err)
	}

	fmt.Println("s02: Tool Use — 在 s01 基础上加了 4 个工具")
	fmt.Println("输入问题，回车发送。输入 q 退出。")
	fmt.Println()

	messages := []*schema.Message{schema.SystemMessage(systemPrompt())}

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\033[36ms02 >> \033[0m")
		if !scanner.Scan() {
			break
		}
		query := strings.TrimSpace(scanner.Text())
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
