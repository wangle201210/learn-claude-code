package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/workspace"
)

type worktreeNameArgs struct {
	Name string `json:"name" jsonschema:"required" jsonschema_description:"Worktree name under .worktrees"`
}

type createWorktreeArgs struct {
	Name   string `json:"name" jsonschema:"required" jsonschema_description:"Worktree name under .worktrees"`
	Branch string `json:"branch,omitempty" jsonschema_description:"Branch to create, defaults to wt/<name>"`
	Base   string `json:"base,omitempty" jsonschema_description:"Base revision, defaults to HEAD"`
}

type removeWorktreeArgs struct {
	Name  string `json:"name" jsonschema:"required" jsonschema_description:"Worktree name under .worktrees"`
	Force bool   `json:"force,omitempty" jsonschema_description:"Remove even if the worktree has local changes"`
}

func (r *Runtime) createWorktree(ctx context.Context, input *createWorktreeArgs) (string, error) {
	name, path, err := r.worktreePath(input.Name)
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(input.Branch)
	if branch == "" {
		branch = "wt/" + name
	}
	base := strings.TrimSpace(input.Base)
	if base == "" {
		base = "HEAD"
	}
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("worktree already exists: %s", path)
	}
	output, err := r.runGit(ctx, "worktree", "add", "-b", branch, path, base)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Created worktree %s at %s.\n%s", name, path, strings.TrimSpace(output)), nil
}

func (r *Runtime) removeWorktree(ctx context.Context, input *removeWorktreeArgs) (string, error) {
	name, path, err := r.worktreePath(input.Name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	if !input.Force {
		status, err := r.runGit(ctx, "-C", path, "status", "--porcelain")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(status) != "" {
			return "", errors.New("worktree has local changes; use force=true or keep_worktree")
		}
	}
	args := []string{"worktree", "remove", path}
	if input.Force {
		args = append(args, "--force")
	}
	output, err := r.runGit(ctx, args...)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Removed worktree %s.\n%s", name, strings.TrimSpace(output)), nil
}

func (r *Runtime) keepWorktree(name string) (string, error) {
	name, path, err := r.worktreePath(name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	keptFile := filepath.Join(r.workdir, ".worktrees", ".kept.jsonl")
	msg := map[string]string{
		"name": name,
		"path": path,
		"time": time.Now().Format(time.RFC3339),
	}
	data, _ := json.Marshal(msg)
	if err := os.MkdirAll(filepath.Dir(keptFile), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(keptFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return "", err
	}
	return fmt.Sprintf("Kept worktree %s at %s.", name, path), nil
}

func (r *Runtime) worktreePath(name string) (string, string, error) {
	name, err := validateMailboxName(name)
	if err != nil {
		return "", "", err
	}
	path, err := workspace.SafePath(filepath.Join(".worktrees", name))
	if err != nil {
		return "", "", err
	}
	return name, path, nil
}

func (r *Runtime) runGit(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.workdir
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", errors.New("git command timed out")
	}
	if err != nil {
		return "", fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
