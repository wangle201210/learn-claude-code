package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/permission"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/workspace"
)

type Runtime struct {
	mu              sync.Mutex
	started         sync.Once
	workdir         string
	nextBackground  int
	background      map[string]*backgroundTask
	crons           map[string]*cronJob
	cronQueue       []string
	cronFile        string
	mailboxDir      string
	mainInboxOffset int64
	teammates       map[string]*teammateRun
	teammateIdle    time.Duration
	taskMu          sync.Mutex
}

type noArgs struct{}

func New(workdir string) *Runtime {
	return &Runtime{
		workdir:      workdir,
		background:   map[string]*backgroundTask{},
		crons:        map[string]*cronJob{},
		cronFile:     filepath.Join(workdir, ".scheduled_tasks.json"),
		mailboxDir:   filepath.Join(workdir, ".mailboxes"),
		teammates:    map[string]*teammateRun{},
		teammateIdle: teammateIdleTimeoutFromEnv(),
	}
}

func teammateIdleTimeoutFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv("FINAL_EINO_TEAMMATE_IDLE_MS"))
	if raw == "" {
		return defaultTeammateIdleTimeout
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms < 0 {
		return defaultTeammateIdleTimeout
	}
	return time.Duration(ms) * time.Millisecond
}

func (r *Runtime) BuildTools(ctx context.Context, primary model.ToolCallingChatModel, workspaceBackend *workspace.Backend, prompt permission.PromptFunc) ([]tool.BaseTool, error) {
	var out []tool.BaseTool
	add := func(t tool.BaseTool, err error) error {
		if err != nil {
			return err
		}
		out = append(out, t)
		return nil
	}

	if err := add(utils.InferTool[*backgroundExecuteArgs, string]("background_execute", "Run a shell command asynchronously and collect its result for later notification.", func(ctx context.Context, input *backgroundExecuteArgs) (string, error) {
		return r.startBackgroundCommand(workspaceBackend, input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*backgroundStatusArgs, string]("background_status", "Check one or all background command results.", func(ctx context.Context, input *backgroundStatusArgs) (string, error) {
		return r.backgroundStatus(input.TaskID), nil
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*spawnTeammateArgs, string]("spawn_teammate", "Spawn a teammate agent in the background and receive its result through notifications.", func(toolCtx context.Context, input *spawnTeammateArgs) (string, error) {
		return r.spawnTeammate(ctx, primary, workspaceBackend, prompt, input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*scheduleCronArgs, string]("schedule_cron", "Schedule a prompt with a five-field cron expression.", func(ctx context.Context, input *scheduleCronArgs) (string, error) {
		return r.scheduleCron(input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*noArgs, string]("list_crons", "List scheduled cron prompts.", func(ctx context.Context, input *noArgs) (string, error) {
		return r.listCrons(), nil
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*cronIDArgs, string]("cancel_cron", "Cancel a scheduled cron prompt.", func(ctx context.Context, input *cronIDArgs) (string, error) {
		return r.cancelCron(input.ID)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*noArgs, string]("check_notifications", "Collect completed background task, cron, and protocol notifications.", func(ctx context.Context, input *noArgs) (string, error) {
		notes := r.CollectNotifications()
		if len(notes) == 0 {
			return "No notifications.", nil
		}
		return strings.Join(notes, "\n\n"), nil
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*sendMessageArgs, string]("send_message", "Send a protocol/mailbox message to another agent or the main agent.", func(ctx context.Context, input *sendMessageArgs) (string, error) {
		return r.sendMessage(input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*checkInboxArgs, string]("check_inbox", "Read recent messages from a mailbox.", func(ctx context.Context, input *checkInboxArgs) (string, error) {
		return r.checkInbox(input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*requestShutdownArgs, string]("request_shutdown", "Request a graceful shutdown or handoff through the protocol mailbox.", func(ctx context.Context, input *requestShutdownArgs) (string, error) {
		return r.requestShutdown(input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*requestPlanArgs, string]("request_plan", "Create a protocol plan-review request.", func(ctx context.Context, input *requestPlanArgs) (string, error) {
		return r.requestPlan(input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*reviewPlanArgs, string]("review_plan", "Submit a protocol review decision for a plan request.", func(ctx context.Context, input *reviewPlanArgs) (string, error) {
		return r.reviewPlan(input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*createWorktreeArgs, string]("create_worktree", "Create an isolated git worktree under .worktrees.", func(ctx context.Context, input *createWorktreeArgs) (string, error) {
		return r.createWorktree(ctx, input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*removeWorktreeArgs, string]("remove_worktree", "Remove a git worktree under .worktrees.", func(ctx context.Context, input *removeWorktreeArgs) (string, error) {
		return r.removeWorktree(ctx, input)
	})); err != nil {
		return nil, err
	}
	if err := add(utils.InferTool[*worktreeNameArgs, string]("keep_worktree", "Mark a git worktree as intentionally kept for later handoff.", func(ctx context.Context, input *worktreeNameArgs) (string, error) {
		return r.keepWorktree(input.Name)
	})); err != nil {
		return nil, err
	}

	return out, nil
}

func (r *Runtime) Start(ctx context.Context) {
	r.started.Do(func() {
		r.loadCrons()
		r.mainInboxOffset = r.currentMailboxSize("main")
		go r.cronLoop(ctx)
	})
}

func (r *Runtime) CollectNotifications() []string {
	r.Start(context.Background())
	return r.collectNotifications()
}

func (r *Runtime) collectNotifications() []string {
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
