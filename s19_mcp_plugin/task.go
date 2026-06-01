package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// tasksDir 是任务图的持久化目录：每个任务一个 task_*.json 文件。
var tasksDir = filepath.Join(workdir, ".tasks")

// Task 是一个可被多个 agent 协作的工作单元；blockedBy 形成有向依赖图。
type Task struct {
	ID          string   `json:"id"`
	Subject     string   `json:"subject"`
	Description string   `json:"description"`
	Status      string   `json:"status"`             // pending | in_progress | completed
	Owner       string   `json:"owner"`              // "" = 未认领
	BlockedBy   []string `json:"blockedBy"`          // 依赖的任务 ID
	Worktree    string   `json:"worktree,omitempty"` // s18：绑定的 worktree 名（空=未绑定）
}

func taskPath(id string) string {
	return filepath.Join(tasksDir, id+".json")
}

func createTask(subject, description string, blockedBy []string) (*Task, error) {
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		return nil, err
	}
	id := fmt.Sprintf("task_%d_%04d", time.Now().Unix(), rand.Intn(10000))
	t := &Task{
		ID:          id,
		Subject:     subject,
		Description: description,
		Status:      "pending",
		BlockedBy:   blockedBy,
	}
	return t, saveTask(t)
}

func saveTask(t *Task) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(taskPath(t.ID), data, 0o644)
}

func loadTask(id string) (*Task, error) {
	data, err := os.ReadFile(taskPath(id))
	if err != nil {
		return nil, err
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// listTasksAll 列出 .tasks/ 下所有任务，按文件名（含时间戳）稳定排序。
func listTasksAll() []*Task {
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, "task_") && strings.HasSuffix(n, ".json") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var out []*Task
	for _, n := range names {
		if t, err := loadTask(strings.TrimSuffix(n, ".json")); err == nil {
			out = append(out, t)
		}
	}
	return out
}

// canStart 检查所有 blockedBy 依赖都已 completed；缺失依赖视为阻塞。
func canStart(t *Task) bool {
	for _, dep := range t.BlockedBy {
		d, err := loadTask(dep)
		if err != nil || d.Status != "completed" {
			return false
		}
	}
	return true
}

func claimTask(id, owner string) string {
	t, err := loadTask(id)
	if err != nil {
		return "Error: " + err.Error()
	}
	if t.Status != "pending" {
		return fmt.Sprintf("Task %s is %s, cannot claim", id, t.Status)
	}
	if !canStart(t) {
		var deps []string
		for _, d := range t.BlockedBy {
			if dep, err := loadTask(d); err != nil || dep.Status != "completed" {
				deps = append(deps, d)
			}
		}
		return fmt.Sprintf("Blocked by: %v", deps)
	}
	t.Owner = owner
	t.Status = "in_progress"
	_ = saveTask(t)
	fmt.Printf("  \033[36m[claim] %s → in_progress (owner: %s)\033[0m\n", t.Subject, owner)
	return fmt.Sprintf("Claimed %s (%s)", t.ID, t.Subject)
}

func completeTask(id string) string {
	t, err := loadTask(id)
	if err != nil {
		return "Error: " + err.Error()
	}
	if t.Status != "in_progress" {
		return fmt.Sprintf("Task %s is %s, cannot complete", id, t.Status)
	}
	t.Status = "completed"
	_ = saveTask(t)

	// 找出因为这次完成而新解锁的下游任务（之前 blockedBy 没全完成，现在全完成了）。
	var unblocked []string
	for _, ot := range listTasksAll() {
		if ot.Status == "pending" && len(ot.BlockedBy) > 0 && canStart(ot) {
			unblocked = append(unblocked, ot.Subject)
		}
	}
	fmt.Printf("  \033[32m[complete] %s ✓\033[0m\n", t.Subject)
	msg := fmt.Sprintf("Completed %s (%s)", t.ID, t.Subject)
	if len(unblocked) > 0 {
		msg += "\nUnblocked: " + strings.Join(unblocked, ", ")
		fmt.Printf("  \033[33m[unblocked] %s\033[0m\n", strings.Join(unblocked, ", "))
	}
	return msg
}

// ── 工具 handlers（绑定到 ToolInfo） ────────────────────────

func runCreateTask(ctx context.Context, args string) string {
	var a struct {
		Subject     string   `json:"subject"`
		Description string   `json:"description"`
		BlockedBy   []string `json:"blockedBy"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	if a.Subject == "" {
		return "Error: subject required"
	}
	t, err := createTask(a.Subject, a.Description, a.BlockedBy)
	if err != nil {
		return "Error: " + err.Error()
	}
	deps := ""
	if len(a.BlockedBy) > 0 {
		deps = " (blockedBy: " + strings.Join(a.BlockedBy, ", ") + ")"
	}
	fmt.Printf("  \033[34m[create] %s%s\033[0m\n", t.Subject, deps)
	return fmt.Sprintf("Created %s: %s%s", t.ID, t.Subject, deps)
}

func runListTasks(ctx context.Context, args string) string {
	tasks := listTasksAll()
	if len(tasks) == 0 {
		return "No tasks. Use create_task to add some."
	}
	var b strings.Builder
	for _, t := range tasks {
		var icon string
		switch t.Status {
		case "pending":
			icon = "○"
		case "in_progress":
			icon = "●"
		case "completed":
			icon = "✓"
		default:
			icon = "?"
		}
		owner := ""
		if t.Owner != "" {
			owner = " [" + t.Owner + "]"
		}
		deps := ""
		if len(t.BlockedBy) > 0 {
			deps = " (blockedBy: " + strings.Join(t.BlockedBy, ", ") + ")"
		}
		fmt.Fprintf(&b, "  %s %s: %s [%s]%s%s\n", icon, t.ID, t.Subject, t.Status, owner, deps)
	}
	return strings.TrimRight(b.String(), "\n")
}

func runGetTask(ctx context.Context, args string) string {
	var a struct {
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	t, err := loadTask(a.TaskID)
	if err != nil {
		return fmt.Sprintf("Error: Task %s not found", a.TaskID)
	}
	data, _ := json.MarshalIndent(t, "", "  ")
	return string(data)
}

func runClaimTask(ctx context.Context, args string) string {
	var a struct {
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	return claimTask(a.TaskID, "agent")
}

func runCompleteTask(ctx context.Context, args string) string {
	var a struct {
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	return completeTask(a.TaskID)
}
