package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	adkfs "github.com/cloudwego/eino/adk/filesystem"
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

type cronJob struct {
	ID        string `json:"id"`
	Cron      string `json:"cron"`
	Prompt    string `json:"prompt"`
	Recurring bool   `json:"recurring"`
	Durable   bool   `json:"durable"`
	LastRun   string `json:"last_run,omitempty"`
	CreatedAt string `json:"created_at"`
}

type backgroundExecuteArgs struct {
	Command        string `json:"command" jsonschema:"required" jsonschema_description:"Shell command to run in the background"`
	Label          string `json:"label,omitempty" jsonschema_description:"Optional human-readable label for the task"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema_description:"Maximum runtime in seconds, default 600 and capped at 3600"`
}

type backgroundStatusArgs struct {
	TaskID string `json:"task_id,omitempty" jsonschema_description:"Specific background task id. Omit to list all tasks"`
}

type scheduleCronArgs struct {
	ID        string `json:"id,omitempty" jsonschema_description:"Optional stable job id"`
	Cron      string `json:"cron" jsonschema:"required" jsonschema_description:"Five-field cron expression: minute hour day-of-month month day-of-week"`
	Prompt    string `json:"prompt" jsonschema:"required" jsonschema_description:"Prompt to inject when the cron fires"`
	Recurring bool   `json:"recurring,omitempty" jsonschema_description:"Whether the job should keep running after it fires"`
	Durable   bool   `json:"durable,omitempty" jsonschema_description:"Persist the job in .scheduled_tasks.json"`
}

type cronIDArgs struct {
	ID string `json:"id" jsonschema:"required" jsonschema_description:"Cron job id"`
}

type noArgs struct{}

type mailboxMessage struct {
	ID       string            `json:"id"`
	Time     string            `json:"time"`
	From     string            `json:"from"`
	To       string            `json:"to"`
	Kind     string            `json:"kind"`
	Subject  string            `json:"subject,omitempty"`
	Body     string            `json:"body"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type sendMessageArgs struct {
	To      string `json:"to" jsonschema:"required" jsonschema_description:"Mailbox recipient name"`
	From    string `json:"from,omitempty" jsonschema_description:"Sender name, defaults to FinalAgent"`
	Kind    string `json:"kind,omitempty" jsonschema_description:"Message kind, such as note, plan_request, shutdown_request"`
	Subject string `json:"subject,omitempty" jsonschema_description:"Short message subject"`
	Body    string `json:"body" jsonschema:"required" jsonschema_description:"Message body"`
}

type checkInboxArgs struct {
	Name  string `json:"name,omitempty" jsonschema_description:"Mailbox name, defaults to main"`
	Limit int    `json:"limit,omitempty" jsonschema_description:"Maximum messages to return, default 20"`
}

type requestShutdownArgs struct {
	Reason string `json:"reason" jsonschema:"required" jsonschema_description:"Why the agent should stop or hand control back"`
}

type requestPlanArgs struct {
	Request string `json:"request" jsonschema:"required" jsonschema_description:"The plan request or decision that needs approval"`
}

type reviewPlanArgs struct {
	RequestID string `json:"request_id" jsonschema:"required" jsonschema_description:"Plan request id"`
	Decision  string `json:"decision" jsonschema:"required,enum=approved,enum=changes_requested,enum=rejected" jsonschema_description:"Review decision"`
	Comments  string `json:"comments,omitempty" jsonschema_description:"Review comments"`
}

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

func (r *agentRuntime) startBackgroundCommand(workspace *workspaceBackend, input *backgroundExecuteArgs) (string, error) {
	if workspace.shell == nil {
		return "", errors.New("shell is not configured")
	}
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

		resp, err := workspace.shell.Execute(ctx, &adkfs.ExecuteRequest{Command: command})
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
				task.Output = truncate(resp.Output, 20000)
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

func (r *agentRuntime) backgroundStatus(taskID string) string {
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

func (r *agentRuntime) scheduleCron(input *scheduleCronArgs) (string, error) {
	if err := validateCron(input.Cron); err != nil {
		return "", err
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return "", errors.New("prompt is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	id := strings.TrimSpace(input.ID)
	if id == "" {
		id = fmt.Sprintf("cron-%d", time.Now().UnixNano())
	}
	if _, err := validateMailboxName(id); err != nil {
		return "", fmt.Errorf("invalid cron id: %w", err)
	}
	r.crons[id] = &cronJob{
		ID:        id,
		Cron:      strings.TrimSpace(input.Cron),
		Prompt:    input.Prompt,
		Recurring: input.Recurring,
		Durable:   input.Durable,
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	r.saveCronsLocked()
	return fmt.Sprintf("Scheduled cron %s.", id), nil
}

func (r *agentRuntime) listCrons() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.crons) == 0 {
		return "No scheduled cron jobs."
	}
	ids := make([]string, 0, len(r.crons))
	for id := range r.crons {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var b strings.Builder
	for _, id := range ids {
		job := r.crons[id]
		fmt.Fprintf(&b, "%s cron=%q recurring=%v durable=%v last_run=%q\nprompt: %s\n\n",
			job.ID, job.Cron, job.Recurring, job.Durable, job.LastRun, job.Prompt)
	}
	return strings.TrimSpace(b.String())
}

func (r *agentRuntime) cancelCron(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.crons[id]; !ok {
		return "No such cron: " + id, nil
	}
	delete(r.crons, id)
	r.saveCronsLocked()
	return "Cancelled cron " + id + ".", nil
}

func (r *agentRuntime) cronLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			r.tickCrons(now)
		}
	}
}

func (r *agentRuntime) tickCrons(now time.Time) {
	if now.Second() != 0 {
		return
	}
	runKey := now.Format("2006-01-02T15:04")

	r.mu.Lock()
	defer r.mu.Unlock()
	changed := false
	for id, job := range r.crons {
		if job.LastRun == runKey || !cronMatches(job.Cron, now) {
			continue
		}
		job.LastRun = runKey
		changed = true
		r.cronQueue = append(r.cronQueue, fmt.Sprintf("<cron_notification id=%q cron=%q>\n%s\n</cron_notification>", id, job.Cron, job.Prompt))
		if !job.Recurring {
			delete(r.crons, id)
		}
	}
	if changed {
		r.saveCronsLocked()
	}
}

func (r *agentRuntime) loadCrons() {
	data, err := os.ReadFile(r.cronFile)
	if err != nil {
		return
	}
	var jobs []*cronJob
	if err := json.Unmarshal(data, &jobs); err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, job := range jobs {
		if job != nil && job.ID != "" {
			r.crons[job.ID] = job
		}
	}
}

func (r *agentRuntime) saveCronsLocked() {
	var jobs []*cronJob
	for _, job := range r.crons {
		if job.Durable {
			jobs = append(jobs, job)
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(r.cronFile, data, 0o644)
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

func (r *agentRuntime) sendMessage(input *sendMessageArgs) (string, error) {
	to, err := validateMailboxName(input.To)
	if err != nil {
		return "", err
	}
	from := strings.TrimSpace(input.From)
	if from == "" {
		from = "FinalAgent"
	}
	kind := strings.TrimSpace(input.Kind)
	if kind == "" {
		kind = "note"
	}
	msg := mailboxMessage{
		ID:      fmt.Sprintf("msg-%d", time.Now().UnixNano()),
		Time:    time.Now().Format(time.RFC3339),
		From:    from,
		To:      to,
		Kind:    kind,
		Subject: input.Subject,
		Body:    input.Body,
	}
	if strings.TrimSpace(msg.Body) == "" {
		return "", errors.New("body is required")
	}
	if err := r.appendMessage(msg); err != nil {
		return "", err
	}
	return fmt.Sprintf("Sent %s to %s.", msg.ID, to), nil
}

func (r *agentRuntime) checkInbox(input *checkInboxArgs) (string, error) {
	name := input.Name
	if name == "" {
		name = "main"
	}
	name, err := validateMailboxName(name)
	if err != nil {
		return "", err
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}
	msgs, err := r.readMailbox(name)
	if err != nil {
		return "", err
	}
	if len(msgs) == 0 {
		return "No messages.", nil
	}
	if len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return formatMessages(msgs), nil
}

func (r *agentRuntime) requestShutdown(input *requestShutdownArgs) (string, error) {
	return r.sendMessage(&sendMessageArgs{
		To:      "main",
		From:    "protocol",
		Kind:    "shutdown_request",
		Subject: "shutdown requested",
		Body:    input.Reason,
	})
}

func (r *agentRuntime) requestPlan(input *requestPlanArgs) (string, error) {
	id := fmt.Sprintf("plan-%d", time.Now().UnixNano())
	if _, err := r.sendMessage(&sendMessageArgs{
		To:      "main",
		From:    "protocol",
		Kind:    "plan_request",
		Subject: id,
		Body:    input.Request,
	}); err != nil {
		return "", err
	}
	return "Created plan request " + id + ".", nil
}

func (r *agentRuntime) reviewPlan(input *reviewPlanArgs) (string, error) {
	decision := strings.TrimSpace(input.Decision)
	if decision != "approved" && decision != "changes_requested" && decision != "rejected" {
		return "", errors.New("decision must be approved, changes_requested, or rejected")
	}
	body := strings.TrimSpace(input.Comments)
	if body == "" {
		body = "decision: " + decision
	}
	return r.sendMessage(&sendMessageArgs{
		To:      "main",
		From:    "protocol",
		Kind:    "plan_review",
		Subject: input.RequestID + ":" + decision,
		Body:    body,
	})
}

func (r *agentRuntime) appendMessage(msg mailboxMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := os.MkdirAll(r.mailboxDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(r.mailboxDir, msg.To+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(msg)
}

func (r *agentRuntime) readMailbox(name string) ([]mailboxMessage, error) {
	data, err := os.ReadFile(filepath.Join(r.mailboxDir, name+".jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var msgs []mailboxMessage
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg mailboxMessage
		if err := json.Unmarshal([]byte(line), &msg); err == nil {
			msgs = append(msgs, msg)
		}
	}
	return msgs, nil
}

func (r *agentRuntime) collectMainInbox() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	path := filepath.Join(r.mailboxDir, "main.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if int64(len(data)) < r.mainInboxOffset {
		r.mainInboxOffset = int64(len(data))
		return nil
	}
	chunk := string(data[r.mainInboxOffset:])
	r.mainInboxOffset = int64(len(data))

	var notes []string
	for _, line := range strings.Split(strings.TrimSpace(chunk), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg mailboxMessage
		if err := json.Unmarshal([]byte(line), &msg); err == nil {
			notes = append(notes, fmt.Sprintf("<message_notification id=%q kind=%q from=%q>\n%s\n</message_notification>", msg.ID, msg.Kind, msg.From, formatMessages([]mailboxMessage{msg})))
		}
	}
	return notes
}

func (r *agentRuntime) currentMailboxSize(name string) int64 {
	path := filepath.Join(r.mailboxDir, name+".jsonl")
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func formatMessages(msgs []mailboxMessage) string {
	var b strings.Builder
	for _, msg := range msgs {
		fmt.Fprintf(&b, "%s [%s] %s -> %s", msg.ID, msg.Kind, msg.From, msg.To)
		if msg.Subject != "" {
			fmt.Fprintf(&b, " subject=%q", msg.Subject)
		}
		fmt.Fprintf(&b, "\n%s\n\n", msg.Body)
	}
	return strings.TrimSpace(b.String())
}

var mailboxNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validateMailboxName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("name is required")
	}
	if !mailboxNameRE.MatchString(name) || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid name %q", name)
	}
	return name, nil
}

func validateCron(expr string) error {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return errors.New("cron must have five fields")
	}
	ranges := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	for i, field := range fields {
		if !cronFieldValid(field, ranges[i][0], ranges[i][1]) {
			return fmt.Errorf("invalid cron field %d: %s", i+1, field)
		}
	}
	return nil
}

func cronMatches(expr string, t time.Time) bool {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return false
	}
	return cronFieldMatches(fields[0], t.Minute(), 0, 59, false) &&
		cronFieldMatches(fields[1], t.Hour(), 0, 23, false) &&
		cronFieldMatches(fields[2], t.Day(), 1, 31, false) &&
		cronFieldMatches(fields[3], int(t.Month()), 1, 12, false) &&
		cronFieldMatches(fields[4], int(t.Weekday()), 0, 7, true)
}

func cronFieldValid(field string, min, max int) bool {
	if field == "" {
		return false
	}
	for _, part := range strings.Split(field, ",") {
		if !cronPartValid(part, min, max) {
			return false
		}
	}
	return true
}

func cronPartValid(part string, min, max int) bool {
	if part == "*" {
		return true
	}
	if strings.HasPrefix(part, "*/") {
		n, err := strconv.Atoi(strings.TrimPrefix(part, "*/"))
		return err == nil && n > 0
	}
	if strings.Contains(part, "-") {
		bounds := strings.SplitN(part, "-", 2)
		if len(bounds) != 2 {
			return false
		}
		start, err1 := strconv.Atoi(bounds[0])
		end, err2 := strconv.Atoi(bounds[1])
		return err1 == nil && err2 == nil && start >= min && end <= max && start <= end
	}
	v, err := strconv.Atoi(part)
	return err == nil && v >= min && v <= max
}

func cronFieldMatches(field string, value, min, max int, dow bool) bool {
	for _, part := range strings.Split(field, ",") {
		if cronPartMatches(part, value, min, max, dow) {
			return true
		}
	}
	return false
}

func cronPartMatches(part string, value, min, max int, dow bool) bool {
	if part == "*" {
		return true
	}
	if strings.HasPrefix(part, "*/") {
		n, err := strconv.Atoi(strings.TrimPrefix(part, "*/"))
		return err == nil && n > 0 && value%n == 0
	}
	if strings.Contains(part, "-") {
		bounds := strings.SplitN(part, "-", 2)
		start, err1 := strconv.Atoi(bounds[0])
		end, err2 := strconv.Atoi(bounds[1])
		if err1 != nil || err2 != nil {
			return false
		}
		if dow {
			if start == 7 {
				start = 0
			}
			if end == 7 {
				end = 0
			}
		}
		if start <= end {
			return value >= start && value <= end
		}
		return value >= min && value <= end || value >= start && value <= max
	}
	v, err := strconv.Atoi(part)
	if err != nil {
		return false
	}
	if dow && v == 7 {
		v = 0
	}
	return value == v
}

func createWorktree(ctx context.Context, input *createWorktreeArgs) (string, error) {
	name, path, err := worktreePath(input.Name)
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
	output, err := runGit(ctx, "worktree", "add", "-b", branch, path, base)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Created worktree %s at %s.\n%s", name, path, strings.TrimSpace(output)), nil
}

func removeWorktree(ctx context.Context, input *removeWorktreeArgs) (string, error) {
	name, path, err := worktreePath(input.Name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	if !input.Force {
		status, err := runGit(ctx, "-C", path, "status", "--porcelain")
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
	output, err := runGit(ctx, args...)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Removed worktree %s.\n%s", name, strings.TrimSpace(output)), nil
}

func keepWorktree(name string) (string, error) {
	name, path, err := worktreePath(name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	keptFile := filepath.Join(workdir, ".worktrees", ".kept.jsonl")
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

func worktreePath(name string) (string, string, error) {
	name, err := validateMailboxName(name)
	if err != nil {
		return "", "", err
	}
	path, err := safePath(filepath.Join(".worktrees", name))
	if err != nil {
		return "", "", err
	}
	return name, path, nil
}

func runGit(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", errors.New("git command timed out")
	}
	if err != nil {
		return "", fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
