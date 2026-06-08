package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/wangle201210/learn-claude-code/internal/cliexit"
)

// s14: 把 messages 提到 session 级，让 queueProcessorLoop 也能在用户输入
// 之外的时间唤醒 agent 处理触发的 cron 任务。sessionMu 防止 REPL 与
// queue processor 同时进入 agentLoop。
var (
	sessionMu      sync.Mutex
	sessionHistory []*schema.Message
	sessionCtx     context.Context
	sessionAgent   model.ToolCallingChatModel
)

// runAgentTurnLocked 跑一次 agent 循环；调用者必须持有 sessionMu。
// query 为空表示这是 queue processor 触发的"无新用户输入"轮——
// agentLoop 顶部仍会从 cronQueue 取走待处理任务。
func runAgentTurnLocked(query string) {
	if query != "" {
		triggerHooks(eventUserPromptSubmit, &hookCtx{query: query})
		sessionHistory = append(sessionHistory, schema.UserMessage(query))
	}
	var err error
	sessionHistory, err = agentLoop(sessionCtx, sessionAgent, sessionHistory)
	if err != nil {
		fmt.Printf("\033[31merror: %v\033[0m\n", err)
		return
	}
	if len(sessionHistory) > 0 {
		fmt.Println(sessionHistory[len(sessionHistory)-1].Content)
		fmt.Println()
	}
}

func runAgentTurn(query string) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	runAgentTurnLocked(query)
}

// queueProcessorLoop 监控 cronQueue：有内容且 agent 空闲时主动唤醒一次 agent。
func queueProcessorLoop() {
	for {
		time.Sleep(200 * time.Millisecond)
		if !hasCronQueue() {
			continue
		}
		if !sessionMu.TryLock() {
			continue
		}
		// 拿到锁后再确认一次（可能其他 turn 已经消化了 queue）。
		if !hasCronQueue() {
			sessionMu.Unlock()
			continue
		}
		fmt.Print("\n  \033[35m[queue processor] delivering scheduled work\033[0m\n")
		runAgentTurnLocked("")
		sessionMu.Unlock()
	}
}

func main() {
	sessionCtx = context.Background()

	base, err := NewModel(sessionCtx)
	if err != nil {
		cliexit.ExitWithError(err)
	}
	sessionAgent, err = base.WithTools(toolInfos())
	if err != nil {
		panic(err)
	}
	subAgent, err = base.WithTools(subToolInfos())
	if err != nil {
		panic(err)
	}
	// s15: teammate 用独立工具集（含 send_message，无 spawn_teammate 防递归）。
	teammateAgent, err = base.WithTools(teammateToolInfos())
	if err != nil {
		panic(err)
	}
	summarizer = base

	// 启动 cron 调度线（加载 durable 作业 + scheduler daemon）+ queue processor。
	startCronOnce()
	go queueProcessorLoop()
	fmt.Println("  \033[35m[queue processor] started\033[0m")

	fmt.Println("s16: Team Protocols — 请求/响应协议 + request_id 状态机")
	fmt.Println("输入问题，回车发送。输入 q 退出。")
	fmt.Println()

	sessionHistory = []*schema.Message{schema.SystemMessage(getSystemPrompt(buildContext()))}

	for {
		query, ok := readLine("\033[36ms16 >> \033[0m")
		if !ok {
			break
		}
		if query == "" || query == "q" || query == "exit" {
			break
		}
		runAgentTurn(query)
	}
}
