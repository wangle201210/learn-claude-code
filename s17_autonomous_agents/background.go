package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// bgTask 跟踪一个后台任务的生命周期：完成后 status 转 "completed" 并填 result。
type bgTask struct {
	toolUseID string
	command   string
	status    string // "running" | "completed"
	result    string
}

var (
	bgMu      sync.Mutex
	bgCounter int
	bgTasks   = map[string]*bgTask{}
)

// isSlowOperation 是 fallback 启发式：bash 命令里含已知慢操作关键词就视为慢。
func isSlowOperation(name, rawArgs string) bool {
	if name != "bash" {
		return false
	}
	var a struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal([]byte(rawArgs), &a)
	cmd := strings.ToLower(a.Command)
	for _, kw := range []string{
		"install", "build", "test", "deploy", "compile",
		"docker build", "pip install", "npm install",
		"cargo build", "pytest", "make",
	} {
		if strings.Contains(cmd, kw) {
			return true
		}
	}
	return false
}

// shouldRunBackground：模型显式 run_in_background=true 最优先；否则用启发式。
func shouldRunBackground(name, rawArgs string) bool {
	var a struct {
		RunInBackground bool `json:"run_in_background"`
	}
	_ = json.Unmarshal([]byte(rawArgs), &a)
	if a.RunInBackground {
		return true
	}
	return isSlowOperation(name, rawArgs)
}

// startBackgroundTask 把工具放进 goroutine，立刻返回 bg_id 作为占位 tool_result。
// 真实结果稍后由 collectBackgroundResults 作为 user notification 注入。
func startBackgroundTask(ctx context.Context, name, toolUseID, rawArgs string, handler toolHandler) string {
	bgMu.Lock()
	bgCounter++
	bgID := fmt.Sprintf("bg_%04d", bgCounter)
	cmd := name
	if name == "bash" {
		var a struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal([]byte(rawArgs), &a)
		cmd = a.Command
	}
	bgTasks[bgID] = &bgTask{toolUseID: toolUseID, command: cmd, status: "running"}
	bgMu.Unlock()

	go func() {
		result := handler(ctx, rawArgs)
		bgMu.Lock()
		if t, ok := bgTasks[bgID]; ok {
			t.status = "completed"
			t.result = result
		}
		bgMu.Unlock()
	}()

	fmt.Printf("  \033[33m[background] dispatched %s: %s\033[0m\n", bgID, truncate(cmd, 40))
	return bgID
}

// collectBackgroundResults 收集所有已完成的后台任务，包装为 <task_notification>
// 字符串列表（顺带从 bgTasks 移除）。
func collectBackgroundResults() []string {
	var ready []*bgTask
	var readyIDs []string

	bgMu.Lock()
	for id, t := range bgTasks {
		if t.status == "completed" {
			ready = append(ready, t)
			readyIDs = append(readyIDs, id)
		}
	}
	for _, id := range readyIDs {
		delete(bgTasks, id)
	}
	bgMu.Unlock()

	notifs := make([]string, 0, len(ready))
	for i, t := range ready {
		id := readyIDs[i]
		summary := truncate(t.result, 200)
		notifs = append(notifs, fmt.Sprintf(
			"<task_notification>\n  <task_id>%s</task_id>\n  <status>completed</status>\n  <command>%s</command>\n  <summary>%s</summary>\n</task_notification>",
			id, t.command, summary,
		))
		fmt.Printf("  \033[32m[background done] %s: %s (%d chars)\033[0m\n", id, truncate(t.command, 40), len(t.result))
	}
	return notifs
}
