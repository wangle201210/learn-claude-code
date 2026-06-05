package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/schema"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/textutil"
)

type RunLogger struct {
	activeTools map[string]time.Time
	lastAgent   string
}

func NewRunLogger() *RunLogger {
	return &RunLogger{activeTools: map[string]time.Time{}}
}

func (l *RunLogger) Log(message string) {
	fmt.Printf("\n\033[90m[%s] %s\033[0m\n", time.Now().Format("15:04:05"), message)
}

func (l *RunLogger) HandleEvent(event *adk.AgentEvent) (adk.Message, error) {
	if event.AgentName != "" && event.AgentName != l.lastAgent {
		l.lastAgent = event.AgentName
		l.Log("进入 agent: " + event.AgentName)
	}
	l.logAction(event.Action)

	if event.Output == nil || event.Output.MessageOutput == nil {
		return nil, nil
	}
	msg, err := event.Output.MessageOutput.GetMessage()
	if err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, nil
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
	return msg, nil
}

func (l *RunLogger) logAction(action *adk.AgentAction) {
	if action == nil {
		return
	}
	l.logCustomizedAction(action.CustomizedAction)
	switch {
	case action.TransferToAgent != nil:
		l.Log("转交给 agent: " + action.TransferToAgent.DestAgentName)
	case action.Interrupted != nil:
		l.Log("执行被中断，等待恢复")
	case action.BreakLoop != nil:
		l.Log("循环 agent 请求结束当前循环")
	case action.Exit:
		l.Log("agent 请求退出")
	}
}

func (l *RunLogger) logCustomizedAction(action any) {
	summaryAction, ok := action.(*summarization.CustomizedAction)
	if !ok || summaryAction == nil {
		return
	}
	switch summaryAction.Type {
	case summarization.ActionTypeBeforeSummarize:
		count := 0
		if summaryAction.Before != nil {
			count = len(summaryAction.Before.Messages)
		}
		l.Log(fmt.Sprintf("开始上下文压缩，原始消息 %d 条", count))
	case summarization.ActionTypeGenerateSummary:
		if summaryAction.GenerateSummary == nil {
			l.Log("正在调用模型生成上下文摘要")
			return
		}
		if err := summaryAction.GenerateSummary.GetError(); err != nil {
			l.Log(fmt.Sprintf("上下文摘要模型调用失败，phase=%s attempt=%d: %v",
				summaryAction.GenerateSummary.Phase,
				summaryAction.GenerateSummary.Attempt,
				err,
			))
			return
		}
		l.Log(fmt.Sprintf("上下文摘要模型调用完成，phase=%s attempt=%d",
			summaryAction.GenerateSummary.Phase,
			summaryAction.GenerateSummary.Attempt,
		))
	case summarization.ActionTypeAfterSummarize:
		count := 0
		if summaryAction.After != nil {
			count = len(summaryAction.After.Messages)
		}
		l.Log(fmt.Sprintf("上下文压缩完成，保留消息 %d 条", count))
	}
}

func (l *RunLogger) logToolStart(call schema.ToolCall) {
	name := call.Function.Name
	if name == "" {
		name = "unknown"
	}
	if call.ID != "" {
		l.activeTools[call.ID] = time.Now()
	}
	l.Log(fmt.Sprintf("调用工具 %s %s", name, summarizeToolArguments(call.Function.Arguments)))
}

func (l *RunLogger) logToolDone(name, callID, content string) {
	if name == "" {
		name = "unknown"
	}
	duration := ""
	if start, ok := l.activeTools[callID]; ok {
		duration = "，耗时 " + time.Since(start).Round(time.Millisecond).String()
		delete(l.activeTools, callID)
	}
	l.Log(fmt.Sprintf("工具完成 %s%s，输出 %d 字符%s", name, duration, len(content), summarizeToolResult(content)))
}

func summarizeToolArguments(args string) string {
	args = strings.TrimSpace(args)
	if args == "" || args == "{}" {
		return ""
	}
	return "参数: " + oneLine(textutil.Truncate(args, 180))
}

func summarizeToolResult(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	return "，摘要: " + oneLine(textutil.Truncate(content, 160))
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
