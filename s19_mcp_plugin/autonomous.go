package main

import (
	"fmt"
	"strings"
	"time"
)

const (
	idlePollInterval = 5 * time.Second
	idleTimeout      = 60 * time.Second
)

// scanUnclaimedTasks 找出 pending、无 owner 且依赖已完成的任务。
func scanUnclaimedTasks() []*Task {
	var out []*Task
	for _, t := range listTasksAll() {
		if t.Status == "pending" && t.Owner == "" && canStart(t) {
			out = append(out, t)
		}
	}
	return out
}

// idlePoll 是 teammate 完成一轮 WORK 后的等待循环：
// 每 5 秒检查一次 inbox 与任务板，最多等 60 秒。
// 返回 "work" / "shutdown" / "timeout"。
//   - dispatchInbox：teammate goroutine 提供的协议消息处理函数（reuse 自 WORK 阶段）
//   - appendUser：让 idlePoll 把 auto-claim 通知作为 user 消息追加回 teammate messages
func idlePoll(name string, dispatchInbox func([]map[string]any) (bool, bool), appendUser func(string)) string {
	deadline := time.Now().Add(idleTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(idlePollInterval)

		// 1) 检查 inbox（含 shutdown_request）
		if inbox := busReadInbox(name); len(inbox) > 0 {
			stop, gotNew := dispatchInbox(inbox)
			if stop {
				return "shutdown"
			}
			if gotNew {
				return "work"
			}
		}

		// 2) 扫任务板，找到 unclaimed 就尝试认领
		for _, t := range scanUnclaimedTasks() {
			result := claimTask(t.ID, name)
			if strings.HasPrefix(result, "Claimed") {
				appendUser(fmt.Sprintf("<auto-claimed>Task %s: %s</auto-claimed>", t.ID, t.Subject))
				fmt.Printf("  \033[32m[idle] %s auto-claimed: %s\033[0m\n", name, t.Subject)
				return "work"
			}
		}
	}
	fmt.Printf("  \033[31m[idle] %s timeout (%ds)\033[0m\n", name, int(idleTimeout.Seconds()))
	return "timeout"
}

