package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

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
