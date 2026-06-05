package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

var stdin = bufio.NewReader(os.Stdin)

func readLine(prompt string) (string, bool) {
	fmt.Print(prompt)
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSpace(line), true
}

// 项目根目录最终版 agent CLI：用 eino adk 抽象替代前 19 章手写的 agent loop。
// 文件总计 ~320 行（s01-s19 累计手写 3800+ 行）。
//
// 启动：
//
//	export OPENAI_API_KEY=... OPENAI_MODEL=... OPENAI_BASE_URL=.../v1
//	# 可选：export OPENAI_FALLBACK_MODEL=...  → 自动启用 Failover
//	go run .
func main() {
	ctx := context.Background()

	primary, err := NewModel(ctx)
	if err != nil {
		panic(err)
	}
	fallback, err := NewFallbackModel(ctx)
	if err != nil {
		panic(err)
	}

	agent, err := BuildAgent(ctx, primary, fallback)
	if err != nil {
		panic(err)
	}

	// EnableStreaming=true：模型生成 token 即推事件，逐 token 打印。
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true,
	})

	fmt.Println("final: eino adk 版 agent（前 19 章手写的等价能力，~320 行）")
	if fallback != nil {
		fmt.Println("       failover 已启用（OPENAI_FALLBACK_MODEL 触发）")
	}
	fmt.Println("输入问题回车发送；q 或 exit 退出。")
	fmt.Println()

	for {
		fmt.Print("\033[36m>> \033[0m")
		line, err := stdin.ReadString('\n')
		if err != nil && line == "" {
			break
		}
		query := strings.TrimSpace(line)
		if query == "" || query == "q" || query == "exit" {
			break
		}

		// runner.Query 内部把字符串包成 user message 再调 Run；返回流式事件迭代器。
		iter := runner.Query(ctx, query)
		for {
			event, ok := iter.Next()
			if !ok {
				break
			}
			if event.Err != nil {
				fmt.Printf("\033[31m%v\033[0m\n", event.Err)
				break
			}
			if event.Output != nil && event.Output.MessageOutput != nil {
				msg, mErr := event.Output.MessageOutput.GetMessage()
				// 只打印 assistant 角色的最终文本——跳过 tool result 事件，避免
				// 与模型转述出来的内容重复显示。
				if mErr == nil && msg != nil && msg.Role == schema.Assistant && msg.Content != "" {
					fmt.Print(msg.Content)
				}
			}
		}
		fmt.Println()
	}
}
