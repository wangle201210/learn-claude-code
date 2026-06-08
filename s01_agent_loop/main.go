package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/wangle201210/learn-claude-code/internal/cliexit"
)

func main() {
	ctx := context.Background()

	base, err := NewModel(ctx)
	if err != nil {
		cliexit.ExitWithError(err)
	}
	// 把 bash 工具绑定到模型，得到一个会调用工具的实例。
	agent, err := base.WithTools([]*schema.ToolInfo{bashTool})
	if err != nil {
		panic(err)
	}

	fmt.Println("s01: Agent Loop")
	fmt.Println("输入问题，回车发送。输入 q 退出。")
	fmt.Println()

	// system 消息作为对话历史的第一条，整个会话共享同一份历史。
	messages := []*schema.Message{schema.SystemMessage(systemPrompt())}

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\033[36ms01 >> \033[0m")
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

		// agentLoop 结束时，最后一条消息就是模型的最终文本回答。
		fmt.Println(messages[len(messages)-1].Content)
		fmt.Println()
	}
}
