package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	adkfs "github.com/cloudwego/eino/adk/filesystem"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/textutil"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/workspace"
)

type backgroundTask struct {
	ID          string     `json:"id"`
	Label       string     `json:"label,omitempty"`
	Command     string     `json:"command"`
	Status      string     `json:"status"`
	Output      string     `json:"output,omitempty"`
	Error       string     `json:"error,omitempty"`
	ExitCode    *int       `json:"exit_code,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Notified    bool       `json:"-"`
}

type backgroundExecuteArgs struct {
	Command        string `json:"command" jsonschema:"required" jsonschema_description:"Shell command to run in the background"`
	Label          string `json:"label,omitempty" jsonschema_description:"Optional human-readable label for the task"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema_description:"Maximum runtime in seconds, default 600 and capped at 3600"`
}

type backgroundStatusArgs struct {
	TaskID string `json:"task_id,omitempty" jsonschema_description:"Specific background task id. Omit to list all tasks"`
}

func (r *Runtime) startBackgroundCommand(workspace *workspace.Backend, input *backgroundExecuteArgs) (string, error) {
	command := strings.TrimSpace(input.Command)
	if command == "" {
		return "", errors.New("command is required")
	}
	timeout := input.TimeoutSeconds
	if timeout <= 0 {
		timeout = 600
	}
	if timeout > 3600 {
		timeout = 3600
	}

	r.mu.Lock()
	r.nextBackground++
	id := fmt.Sprintf("bg-%d", r.nextBackground)
	task := &backgroundTask{
		ID:        id,
		Label:     input.Label,
		Command:   command,
		Status:    "running",
		StartedAt: time.Now(),
	}
	r.background[id] = task
	r.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
		defer cancel()

		resp, err := workspace.ExecuteBackground(ctx, &adkfs.ExecuteRequest{Command: command})
		now := time.Now()

		r.mu.Lock()
		defer r.mu.Unlock()
		if err != nil {
			task.Status = "failed"
			task.Error = err.Error()
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			task.Status = "failed"
			task.Error = fmt.Sprintf("timeout after %ds", timeout)
		} else {
			task.Status = "succeeded"
			if resp != nil {
				task.Output = textutil.Truncate(resp.Output, 20000)
				task.ExitCode = resp.ExitCode
				if resp.ExitCode != nil && *resp.ExitCode != 0 {
					task.Status = "failed"
				}
			}
		}
		task.CompletedAt = &now
	}()

	return fmt.Sprintf("Started background task %s.", id), nil
}

func (r *Runtime) backgroundStatus(taskID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if taskID != "" {
		task, ok := r.background[taskID]
		if !ok {
			return "No such background task: " + taskID
		}
		return formatBackgroundTask(task, true)
	}

	ids := sortedBackgroundIDs(r.background)
	if len(ids) == 0 {
		return "No background tasks."
	}
	var parts []string
	for _, id := range ids {
		parts = append(parts, formatBackgroundTask(r.background[id], false))
	}
	return strings.Join(parts, "\n\n")
}

func sortedBackgroundIDs(tasks map[string]*backgroundTask) []string {
	ids := make([]string, 0, len(tasks))
	for id := range tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func formatBackgroundTask(task *backgroundTask, includeOutput bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s]", task.ID, task.Status)
	if task.Label != "" {
		fmt.Fprintf(&b, " %s", task.Label)
	}
	fmt.Fprintf(&b, "\ncommand: %s", task.Command)
	if task.CompletedAt != nil {
		fmt.Fprintf(&b, "\ncompleted_at: %s", task.CompletedAt.Format(time.RFC3339))
	}
	if task.ExitCode != nil {
		fmt.Fprintf(&b, "\nexit_code: %d", *task.ExitCode)
	}
	if task.Error != "" {
		fmt.Fprintf(&b, "\nerror: %s", task.Error)
	}
	if includeOutput && task.Output != "" {
		fmt.Fprintf(&b, "\noutput:\n%s", task.Output)
	}
	return b.String()
}
