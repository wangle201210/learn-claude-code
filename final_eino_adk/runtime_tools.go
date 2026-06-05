package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

var runtimeState = newAgentRuntime()

type agentRuntime struct {
	mu              sync.Mutex
	started         sync.Once
	nextBackground  int
	background      map[string]*backgroundTask
	crons           map[string]*cronJob
	cronQueue       []string
	cronFile        string
	mailboxDir      string
	mainInboxOffset int64
}

type noArgs struct{}

func newAgentRuntime() *agentRuntime {
	return &agentRuntime{
		background: map[string]*backgroundTask{},
		crons:      map[string]*cronJob{},
		cronFile:   filepath.Join(workdir, ".scheduled_tasks.json"),
		mailboxDir: filepath.Join(workdir, ".mailboxes"),
	}
}

func buildRuntimeTools(_ context.Context, workspace *workspaceBackend) ([]tool.BaseTool, error) {
	var out []tool.BaseTool
	add := func(t tool.BaseTool, err error) error {
		if err != nil {
			return err
		}
		out = append(out, t)
		return nil
	}

	if err := add(utils.InferTool[*backgroundExecuteArgs, string]("background_execute", "Run a shell command asynchronously and collect its result for later notification.", func(ctx context.Context, input *backgroundExecuteArgs) (string, error) {
		return runtimeState.startBackgroundCommand(workspace, input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*backgroundStatusArgs, string]("background_status", "Check one or all background command results.", func(ctx context.Context, input *backgroundStatusArgs) (string, error) {
		return runtimeState.backgroundStatus(input.TaskID), nil
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*scheduleCronArgs, string]("schedule_cron", "Schedule a prompt with a five-field cron expression.", func(ctx context.Context, input *scheduleCronArgs) (string, error) {
		return runtimeState.scheduleCron(input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*noArgs, string]("list_crons", "List scheduled cron prompts.", func(ctx context.Context, input *noArgs) (string, error) {
		return runtimeState.listCrons(), nil
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*cronIDArgs, string]("cancel_cron", "Cancel a scheduled cron prompt.", func(ctx context.Context, input *cronIDArgs) (string, error) {
		return runtimeState.cancelCron(input.ID)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*noArgs, string]("check_notifications", "Collect completed background task, cron, and protocol notifications.", func(ctx context.Context, input *noArgs) (string, error) {
		notes := collectRuntimeNotifications()
		if len(notes) == 0 {
			return "No notifications.", nil
		}
		return strings.Join(notes, "\n\n"), nil
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*sendMessageArgs, string]("send_message", "Send a protocol/mailbox message to another agent or the main agent.", func(ctx context.Context, input *sendMessageArgs) (string, error) {
		return runtimeState.sendMessage(input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*checkInboxArgs, string]("check_inbox", "Read recent messages from a mailbox.", func(ctx context.Context, input *checkInboxArgs) (string, error) {
		return runtimeState.checkInbox(input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*requestShutdownArgs, string]("request_shutdown", "Request a graceful shutdown or handoff through the protocol mailbox.", func(ctx context.Context, input *requestShutdownArgs) (string, error) {
		return runtimeState.requestShutdown(input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*requestPlanArgs, string]("request_plan", "Create a protocol plan-review request.", func(ctx context.Context, input *requestPlanArgs) (string, error) {
		return runtimeState.requestPlan(input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*reviewPlanArgs, string]("review_plan", "Submit a protocol review decision for a plan request.", func(ctx context.Context, input *reviewPlanArgs) (string, error) {
		return runtimeState.reviewPlan(input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*createWorktreeArgs, string]("create_worktree", "Create an isolated git worktree under .worktrees.", func(ctx context.Context, input *createWorktreeArgs) (string, error) {
		return createWorktree(ctx, input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*removeWorktreeArgs, string]("remove_worktree", "Remove a git worktree under .worktrees.", func(ctx context.Context, input *removeWorktreeArgs) (string, error) {
		return removeWorktree(ctx, input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*worktreeNameArgs, string]("keep_worktree", "Mark a git worktree as intentionally kept for later handoff.", func(ctx context.Context, input *worktreeNameArgs) (string, error) {
		return keepWorktree(input.Name)
	})); err != nil {
		return nil, err
	}

	return out, nil
}

func (r *agentRuntime) start(ctx context.Context) {
	r.started.Do(func() {
		r.loadCrons()
		r.mainInboxOffset = r.currentMailboxSize("main")
		go r.cronLoop(ctx)
	})
}

func collectRuntimeNotifications() []string {
	runtimeState.start(context.Background())
	return runtimeState.collectNotifications()
}

func (r *agentRuntime) collectNotifications() []string {
	var notes []string

	r.mu.Lock()
	for _, id := range sortedBackgroundIDs(r.background) {
		task := r.background[id]
		if task.Status != "running" && !task.Notified {
			notes = append(notes, fmt.Sprintf("<task_notification id=%q status=%q>\n%s\n</task_notification>", task.ID, task.Status, formatBackgroundTask(task, true)))
			task.Notified = true
		}
	}
	if len(r.cronQueue) > 0 {
		notes = append(notes, r.cronQueue...)
		r.cronQueue = nil
	}
	r.mu.Unlock()

	notes = append(notes, r.collectMainInbox()...)
	return notes
}
