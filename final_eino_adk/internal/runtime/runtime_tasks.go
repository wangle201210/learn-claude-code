package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type boardTask struct {
	ID          string         `json:"id"`
	Subject     string         `json:"subject"`
	Description string         `json:"description"`
	Status      string         `json:"status"`
	Blocks      []string       `json:"blocks"`
	BlockedBy   []string       `json:"blockedBy"`
	ActiveForm  string         `json:"activeForm,omitempty"`
	Owner       string         `json:"owner,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

func taskBoardDir(root string) string {
	return filepath.Join(root, ".tasks")
}

func listBoardTasks(root string) ([]*boardTask, error) {
	entries, err := os.ReadDir(taskBoardDir(root))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var tasks []*boardTask
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !isBoardTaskID(id) {
			continue
		}
		task, err := loadBoardTask(root, id)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return tasks, nil
}

func loadBoardTask(root, id string) (*boardTask, error) {
	if !isBoardTaskID(id) {
		return nil, fmt.Errorf("invalid task id %q", id)
	}
	data, err := os.ReadFile(filepath.Join(taskBoardDir(root), id+".json"))
	if err != nil {
		return nil, err
	}
	var task boardTask
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, err
	}
	if task.ID == "" {
		task.ID = id
	}
	return &task, nil
}

func saveBoardTask(root string, task *boardTask) error {
	if task == nil || !isBoardTaskID(task.ID) {
		return fmt.Errorf("invalid task")
	}
	data, err := json.Marshal(task)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(taskBoardDir(root), task.ID+".json"), data, 0o644)
}

func claimNextBoardTask(root, owner string) (*boardTask, bool, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil, false, errors.New("owner is required")
	}
	tasks, err := listBoardTasks(root)
	if err != nil {
		return nil, false, err
	}
	taskByID := make(map[string]*boardTask, len(tasks))
	for _, task := range tasks {
		taskByID[task.ID] = task
	}
	for _, task := range tasks {
		if task.Status != "pending" || task.Owner != "" || !boardTaskCanStart(task, taskByID) {
			continue
		}
		task.Status = "in_progress"
		task.Owner = owner
		if task.Metadata == nil {
			task.Metadata = map[string]any{}
		}
		task.Metadata["claimedAt"] = time.Now().Format(time.RFC3339)
		if err := saveBoardTask(root, task); err != nil {
			return nil, false, err
		}
		return task, true, nil
	}
	return nil, false, nil
}

func boardTaskCanStart(task *boardTask, taskByID map[string]*boardTask) bool {
	for _, depID := range task.BlockedBy {
		dep := taskByID[depID]
		if dep == nil || dep.Status != "completed" {
			return false
		}
	}
	return true
}

func isBoardTaskID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
