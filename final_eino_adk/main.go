package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cloudwego/eino/adk"
	agentapp "github.com/wangle201210/learn-claude-code/final_eino_adk/internal/agent"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/cli"
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

	history := &conversationHistory{}
	agent, runtimeState, err := agentapp.Build(ctx, primary, fallback, readLine, history.replace)
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
		if query == "q" || query == "exit" {
			break
		}
		notifications := runtimeState.CollectNotifications()
		if query == "" && len(notifications) == 0 {
			break
		}
		if len(notifications) > 0 {
			query = strings.Join(notifications, "\n\n") + "\n\n" + query
		}

		history.beginRound()
		input, userMessage := history.nextInput(query)
		logger := cli.NewRunLogger()
		logger.Log("开始处理请求")

		// Runner.Query 每次都会新建 ADK run session；这里显式传入历史，保证多轮上下文连续。
		iter := runner.Run(ctx, input)
		var roundMessages []adk.Message
		for {
			event, ok := iter.Next()
			if !ok {
				break
			}
			if event.Err != nil {
				logger.Log("执行出错")
				fmt.Printf("\033[31m%v\033[0m\n", event.Err)
				break
			}
			msg, err := logger.HandleEvent(event)
			if err != nil {
				fmt.Printf("\033[31m%v\033[0m\n", err)
				break
			}
			if msg != nil {
				roundMessages = append(roundMessages, msg)
			}
		}
		history.commitFallback(userMessage, roundMessages)
		fmt.Println()
	}
}
