package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	worktreesDir = filepath.Join(workdir, ".worktrees")
	eventsPath   = filepath.Join(worktreesDir, "events.jsonl")
	validWTName  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

// validateWorktreeName 拒绝空、`.`/`..` 与非法字符。返回错误信息或空串。
func validateWorktreeName(name string) string {
	if name == "" {
		return "Worktree name cannot be empty"
	}
	if name == "." || name == ".." {
		return fmt.Sprintf("'%s' is not a valid worktree name", name)
	}
	if !validWTName.MatchString(name) {
		return fmt.Sprintf("Invalid worktree name '%s': only letters, digits, dots, underscores, dashes (1-64 chars)", name)
	}
	return ""
}

// runGit 在 workdir 运行 git 命令；返回 (ok, output)；30s 超时。
func runGit(args ...string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if len(s) > 5000 {
		s = s[:5000]
	}
	if s == "" {
		s = "(no output)"
	}
	return err == nil, s
}

// logEvent 把生命周期事件追加到 .worktrees/events.jsonl。
func logEvent(eventType, name, taskID string) {
	_ = os.MkdirAll(worktreesDir, 0o755)
	ev := map[string]any{
		"type":     eventType,
		"worktree": name,
		"task_id":  taskID,
		"ts":       time.Now().Unix(),
	}
	data, _ := json.Marshal(ev)
	f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

// createWorktree：校验名字 → git worktree add .worktrees/{name} -b wt/{name} HEAD
// 可选绑定 task。事件仅在成功时写入。
func createWorktree(name, taskID string) string {
	if e := validateWorktreeName(name); e != "" {
		return "Error: " + e
	}
	path := filepath.Join(worktreesDir, name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Sprintf("Worktree '%s' already exists at %s", name, path)
	}
	_ = os.MkdirAll(worktreesDir, 0o755)
	ok, out := runGit("worktree", "add", path, "-b", "wt/"+name, "HEAD")
	if !ok {
		return "Git error: " + out
	}
	if taskID != "" {
		bindTaskToWorktree(taskID, name)
	}
	logEvent("create", name, taskID)
	fmt.Printf("  \033[33m[worktree] created: %s at %s\033[0m\n", name, path)
	return fmt.Sprintf("Worktree '%s' created at %s", name, path)
}

// bindTaskToWorktree 把 task.Worktree 写为指定名字（task 状态保持 pending 让自治认领）。
func bindTaskToWorktree(taskID, wtName string) {
	t, err := loadTask(taskID)
	if err != nil {
		return
	}
	t.Worktree = wtName
	_ = saveTask(t)
	fmt.Printf("  \033[33m[bind] %s → worktree:%s\033[0m\n", t.Subject, wtName)
}

// countWorktreeChanges 数 worktree 下未提交文件与未推送 commit。
func countWorktreeChanges(path string) (int, int) {
	files := -1
	commits := -1

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = path
	if out, err := cmd.Output(); err == nil {
		files = 0
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if strings.TrimSpace(line) != "" {
				files++
			}
		}
	}

	cmd = exec.CommandContext(ctx, "git", "log", "@{push}..HEAD", "--oneline")
	cmd.Dir = path
	if out, err := cmd.Output(); err == nil {
		commits = 0
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if strings.TrimSpace(line) != "" {
				commits++
			}
		}
	}
	return files, commits
}

// removeWorktree：默认拒绝有未提交改动的 worktree；discard=true 才强制删除。
func removeWorktree(name string, discard bool) string {
	if e := validateWorktreeName(name); e != "" {
		return e
	}
	path := filepath.Join(worktreesDir, name)
	if _, err := os.Stat(path); err != nil {
		return fmt.Sprintf("Worktree '%s' not found", name)
	}
	if !discard {
		files, commits := countWorktreeChanges(path)
		if files < 0 {
			return fmt.Sprintf("Cannot verify worktree '%s' status. Use discard_changes=true to force removal.", name)
		}
		if files > 0 || commits > 0 {
			return fmt.Sprintf("Worktree '%s' has %d uncommitted file(s) and %d unpushed commit(s). "+
				"Use discard_changes=true to force removal, or keep_worktree to preserve for review.", name, files, commits)
		}
	}
	if ok, out := runGit("worktree", "remove", path, "--force"); !ok {
		return "Failed to remove worktree directory for '" + name + "': " + out
	}
	runGit("branch", "-D", "wt/"+name)
	logEvent("remove", name, "")
	fmt.Printf("  \033[33m[worktree] removed: %s\033[0m\n", name)
	return fmt.Sprintf("Worktree '%s' removed", name)
}

// keepWorktree：保留 worktree 与分支，仅写一条 keep 事件。
func keepWorktree(name string) string {
	if e := validateWorktreeName(name); e != "" {
		return e
	}
	logEvent("keep", name, "")
	fmt.Printf("  \033[36m[worktree] kept: %s\033[0m\n", name)
	return fmt.Sprintf("Worktree '%s' kept for review (branch: wt/%s)", name, name)
}

// ── Lead 端的工具 handlers ─────────────────────────────────

func runCreateWorktree(ctx context.Context, args string) string {
	var a struct {
		Name   string `json:"name"`
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	if a.Name == "" {
		return "Error: 'name' required"
	}
	return createWorktree(a.Name, a.TaskID)
}

func runRemoveWorktree(ctx context.Context, args string) string {
	var a struct {
		Name           string `json:"name"`
		DiscardChanges bool   `json:"discard_changes"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	if a.Name == "" {
		return "Error: 'name' required"
	}
	return removeWorktree(a.Name, a.DiscardChanges)
}

func runKeepWorktree(ctx context.Context, args string) string {
	var a struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	if a.Name == "" {
		return "Error: 'name' required"
	}
	return keepWorktree(a.Name)
}
