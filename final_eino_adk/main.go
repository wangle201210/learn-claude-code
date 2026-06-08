package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cloudwego/eino/adk"
	agentapp "github.com/wangle201210/learn-claude-code/final_eino_adk/internal/agent"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/cli"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/recovery"
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
	compactController := agentapp.NewCompactController()
	agent, runtimeState, err := agentapp.Build(ctx, primary, fallback, readLine, history.replace, compactController)
	if err != nil {
		panic(err)
	}

	// EnableStreaming=true：模型生成 token 即推事件，逐 token 打印。
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true,
	})

	if fallback != nil {
		fmt.Println("       failover 已启用（OPENAI_FALLBACK_MODEL 触发）")
	}
	if summary := modelRoutingSummaryFromEnv(); summary != "" {
		fmt.Println("       " + summary)
	}
	fmt.Println("输入问题回车发送；/compact 手动压缩上下文；q 或 exit 退出。")
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
		if isManualCompact(query) {
			history.beginRound()
			input := history.copyMessages()
			if len(input) == 0 {
				fmt.Println("\033[90m[compact] 当前没有可压缩的上下文\033[0m")
				fmt.Println()
				continue
			}
			compactController.RequestWithPrompt("Context compaction is complete. Reply in one short Chinese sentence confirming the conversation history was compacted.")
			runAgentWithRecovery(ctx, runner, history, compactController, input, nil, "手动压缩上下文")
			continue
		}
		var shouldRun bool
		query, shouldRun = prepareQuery(query, runtimeState.CollectNotifications())
		if !shouldRun {
			continue
		}

		history.beginRound()
		input, userMessage := history.nextInput(query)
		runAgentWithRecovery(ctx, runner, history, compactController, input, userMessage, "开始处理请求")
	}
}

func prepareQuery(query string, notifications []string) (string, bool) {
	if query == "" && len(notifications) == 0 {
		return "", false
	}
	if len(notifications) > 0 {
		return strings.Join(notifications, "\n\n") + "\n\n" + query, true
	}
	return query, true
}

func isManualCompact(query string) bool {
	switch strings.ToLower(strings.TrimSpace(query)) {
	case "/compact", "compact":
		return true
	default:
		return false
	}
}

type agentRunResult struct {
	Messages []adk.Message
	Err      error
}

func runAgent(ctx context.Context, runner *adk.Runner, history *conversationHistory, input []adk.Message, userMessage adk.Message, startLog string) agentRunResult {
	logger := cli.NewRunLogger()
	logger.Log(startLog)
	runCtx, routeUsage := withModelRouteUsage(ctx)

	// Runner.Run 每次都会新建 ADK run session；这里显式传入历史，保证多轮上下文连续。
	iter := runner.Run(runCtx, input)
	var roundMessages []adk.Message
	var runErr error
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			runErr = event.Err
			if !isRecoveringRetryError(event.Err) {
				logger.Log("执行出错")
				fmt.Printf("\033[31m%v\033[0m\n", event.Err)
				break
			}
			logger.Log("模型输出被拒绝，正在按 Eino retry 策略自动恢复")
			runErr = nil
			continue
		}
		msg, err := logger.HandleEvent(event)
		if err != nil {
			runErr = err
			if isRecoveringRetryError(err) {
				logger.Log("模型输出被拒绝，正在按 Eino retry 策略自动恢复")
				runErr = nil
				continue
			}
			fmt.Printf("\033[31m%v\033[0m\n", err)
			break
		}
		if msg != nil {
			roundMessages = append(roundMessages, msg)
		}
	}
	if summary := formatModelRouteUsage(routeUsage); summary != "" {
		logger.Log(summary)
	}
	if runErr == nil || !recovery.IsPromptTooLong(runErr) {
		history.commitFallback(userMessage, roundMessages)
	}
	fmt.Println()
	return agentRunResult{Messages: roundMessages, Err: runErr}
}

func runAgentWithRecovery(ctx context.Context, runner *adk.Runner, history *conversationHistory, compactController *agentapp.CompactController, input []adk.Message, userMessage adk.Message, startLog string) agentRunResult {
	result := runAgent(ctx, runner, history, input, userMessage, startLog)
	if result.Err == nil || !recovery.IsPromptTooLong(result.Err) {
		return result
	}
	if compactController == nil {
		return result
	}

	cli.NewRunLogger().Log("上下文过长，触发反应式压缩后重试")
	compactController.Request()
	retryInput := append(copyMessages(input), result.Messages...)
	if len(retryInput) == 0 {
		return result
	}
	return runAgent(ctx, runner, history, retryInput, userMessage, "反应式压缩后重试")
}

func isRecoveringRetryError(err error) bool {
	var willRetry *adk.WillRetryError
	return errors.As(err, &willRetry)
}
