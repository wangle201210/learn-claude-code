package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

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
		if query == "q" || query == "exit" {
			break
		}
		notifications := collectRuntimeNotifications()
		if query == "" && len(notifications) == 0 {
			break
		}
		if len(notifications) > 0 {
			query = strings.Join(notifications, "\n\n") + "\n\n" + query
		}

		logger := newRunLogger()
		logger.log("开始处理请求")

		// runner.Query 内部把字符串包成 user message 再调 Run；返回流式事件迭代器。
		iter := runner.Query(ctx, query)
		for {
			event, ok := iter.Next()
			if !ok {
				break
			}
			if event.Err != nil {
				logger.log("执行出错")
				fmt.Printf("\033[31m%v\033[0m\n", event.Err)
				break
			}
			if err := logger.handleEvent(event); err != nil {
				fmt.Printf("\033[31m%v\033[0m\n", err)
				break
			}
		}
		fmt.Println()
	}
}

type runLogger struct {
	activeTools map[string]time.Time
	lastAgent   string
}

func newRunLogger() *runLogger {
	return &runLogger{activeTools: map[string]time.Time{}}
}

func (l *runLogger) log(message string) {
	fmt.Printf("\n\033[90m[%s] %s\033[0m\n", time.Now().Format("15:04:05"), message)
}

func (l *runLogger) handleEvent(event *adk.AgentEvent) error {
	if event.AgentName != "" && event.AgentName != l.lastAgent {
		l.lastAgent = event.AgentName
		l.log("进入 agent: " + event.AgentName)
	}
	l.logAction(event.Action)

	if event.Output == nil || event.Output.MessageOutput == nil {
		return nil
	}
	msg, err := event.Output.MessageOutput.GetMessage()
	if err != nil {
		return err
	}
	if msg == nil {
		return nil
	}

	switch msg.Role {
	case schema.Assistant:
		if msg.Content != "" {
			fmt.Print(msg.Content)
		}
		for _, call := range msg.ToolCalls {
			l.logToolStart(call)
		}
	case schema.Tool:
		toolName := event.Output.MessageOutput.ToolName
		if toolName == "" {
			toolName = msg.ToolName
		}
		l.logToolDone(toolName, msg.ToolCallID, msg.Content)
	}
	return nil
}

func (l *runLogger) logAction(action *adk.AgentAction) {
	if action == nil {
		return
	}
	switch {
	case action.TransferToAgent != nil:
		l.log("转交给 agent: " + action.TransferToAgent.DestAgentName)
	case action.Interrupted != nil:
		l.log("执行被中断，等待恢复")
	case action.BreakLoop != nil:
		l.log("循环 agent 请求结束当前循环")
	case action.Exit:
		l.log("agent 请求退出")
	}
}

func (l *runLogger) logToolStart(call schema.ToolCall) {
	name := call.Function.Name
	if name == "" {
		name = "unknown"
	}
	if call.ID != "" {
		l.activeTools[call.ID] = time.Now()
	}
	l.log(fmt.Sprintf("调用工具 %s %s", name, summarizeToolArguments(call.Function.Arguments)))
}

func (l *runLogger) logToolDone(name, callID, content string) {
	if name == "" {
		name = "unknown"
	}
	duration := ""
	if start, ok := l.activeTools[callID]; ok {
		duration = "，耗时 " + time.Since(start).Round(time.Millisecond).String()
		delete(l.activeTools, callID)
	}
	l.log(fmt.Sprintf("工具完成 %s%s，输出 %d 字符%s", name, duration, len(content), summarizeToolResult(content)))
}

func summarizeToolArguments(args string) string {
	args = strings.TrimSpace(args)
	if args == "" || args == "{}" {
		return ""
	}
	return "参数: " + oneLine(truncate(args, 180))
}

func summarizeToolResult(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	return "，摘要: " + oneLine(truncate(content, 160))
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
