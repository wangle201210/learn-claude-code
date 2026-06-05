package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/modelroute"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/permission"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/workspace"
)

func TestSpawnTeammateWritesResultToMainMailbox(t *testing.T) {
	ctx := context.Background()
	r := New(t.TempDir())
	r.teammateIdle = 20 * time.Millisecond
	r.Start(ctx)
	backend, err := workspace.New(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	model := &teammateTestModel{}

	out, err := r.spawnTeammate(ctx, model, backend, denyPrompt, &spawnTeammateArgs{
		Name:   "reviewer",
		Role:   "reviewer",
		Prompt: "review the plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `Spawned teammate "reviewer"`) {
		t.Fatalf("spawn output = %q", out)
	}

	notes := waitRuntimeNotifications(r, time.Second, "teammate_result")
	if len(notes) != 1 {
		t.Fatalf("notifications = %v, want one teammate result", notes)
	}
	if !strings.Contains(notes[0], `kind="teammate_result"`) || !strings.Contains(notes[0], "teammate result") {
		t.Fatalf("notification = %q", notes[0])
	}
}

func TestSpawnTeammateNotificationIncludesModelRouteUsage(t *testing.T) {
	ctx := context.Background()
	r := New(t.TempDir())
	r.teammateIdle = 20 * time.Millisecond
	r.Start(ctx)
	backend, err := workspace.New(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	model := &routeUsageTeammateTestModel{usage: &schema.TokenUsage{
		PromptTokens:     4,
		CompletionTokens: 3,
		TotalTokens:      7,
	}}

	if _, err := r.spawnTeammate(ctx, model, backend, denyPrompt, &spawnTeammateArgs{
		Name:   "reviewer",
		Role:   "reviewer",
		Prompt: "review the plan",
	}); err != nil {
		t.Fatal(err)
	}

	notes := waitRuntimeNotifications(r, time.Second, "model route 本轮")
	if len(notes) != 1 {
		t.Fatalf("notifications = %v, want one teammate result", notes)
	}
	if !strings.Contains(notes[0], "simple=1(simple-model){tokens=7,in=4,out=3}") {
		t.Fatalf("notification missing model route usage: %q", notes[0])
	}
}

func TestSpawnTeammateRejectsDuplicateRunningName(t *testing.T) {
	ctx := context.Background()
	r := New(t.TempDir())
	r.teammateIdle = 20 * time.Millisecond
	r.Start(ctx)
	backend, err := workspace.New(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	model := &blockingTeammateTestModel{started: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() { close(model.release) })

	if _, err := r.spawnTeammate(ctx, model, backend, denyPrompt, &spawnTeammateArgs{
		Name:   "worker",
		Prompt: "hold",
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-model.started:
	case <-time.After(time.Second):
		t.Fatal("teammate model did not start")
	}

	out, err := r.spawnTeammate(ctx, &teammateTestModel{}, backend, denyPrompt, &spawnTeammateArgs{
		Name:   "worker",
		Prompt: "again",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != `Teammate "worker" is already running.` {
		t.Fatalf("duplicate output = %q", out)
	}
}

func TestTeammateAutoClaimsAvailableTask(t *testing.T) {
	ctx := context.Background()
	r := New(t.TempDir())
	r.teammateIdle = 20 * time.Millisecond
	r.Start(ctx)
	backend, err := workspace.New(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	model := &recordingTeammateTestModel{}
	writeTestBoardTask(t, r.workdir, &boardTask{
		ID:          "1",
		Subject:     "Review parser",
		Description: "Check parser behavior",
		Status:      "pending",
		Blocks:      []string{},
		BlockedBy:   []string{},
	})

	if _, err := r.spawnTeammate(ctx, model, backend, denyPrompt, &spawnTeammateArgs{
		Name:   "worker",
		Prompt: "initial task",
	}); err != nil {
		t.Fatal(err)
	}

	notes := waitRuntimeNotifications(r, 2*time.Second, "Auto-claimed task #1")
	task, err := loadBoardTask(r.workdir, "1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Owner != "worker" || task.Status != "in_progress" {
		t.Fatalf("task after auto-claim = owner %q status %q, want worker/in_progress", task.Owner, task.Status)
	}
	if !model.sawPrompt("auto-claimed") {
		t.Fatalf("teammate prompts = %v, want auto-claimed task prompt", model.prompts())
	}
	if !containsText(notes, "Auto-claimed task #1") {
		t.Fatalf("notifications = %v, want auto-claimed task result", notes)
	}
}

func TestClaimNextBoardTaskHonorsBlockedBy(t *testing.T) {
	root := t.TempDir()
	writeTestBoardTask(t, root, &boardTask{
		ID:          "1",
		Subject:     "Dependency",
		Description: "Must finish first",
		Status:      "pending",
		Blocks:      []string{"2"},
		BlockedBy:   []string{},
	})
	writeTestBoardTask(t, root, &boardTask{
		ID:          "2",
		Subject:     "Blocked",
		Description: "Waits on dependency",
		Status:      "pending",
		Blocks:      []string{},
		BlockedBy:   []string{"1"},
	})

	task, ok, err := claimNextBoardTask(root, "worker")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || task.ID != "1" {
		t.Fatalf("claimed task = %#v ok=%v, want task 1", task, ok)
	}

	task, ok, err = claimNextBoardTask(root, "worker")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("claimed blocked task = %#v, want none", task)
	}
}

func TestTeammateIdleTimeoutFromEnv(t *testing.T) {
	t.Setenv("FINAL_EINO_TEAMMATE_IDLE_MS", "0")
	if got := teammateIdleTimeoutFromEnv(); got != 0 {
		t.Fatalf("idle timeout = %v, want 0", got)
	}
	t.Setenv("FINAL_EINO_TEAMMATE_IDLE_MS", "35")
	if got := teammateIdleTimeoutFromEnv(); got != 35*time.Millisecond {
		t.Fatalf("idle timeout = %v, want 35ms", got)
	}
	t.Setenv("FINAL_EINO_TEAMMATE_IDLE_MS", "-1")
	if got := teammateIdleTimeoutFromEnv(); got != defaultTeammateIdleTimeout {
		t.Fatalf("idle timeout = %v, want default", got)
	}
}

func waitForTeammateStatus(r *Runtime, name, status string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		run, ok := r.teammates[name]
		done := ok && run.Status == status
		r.mu.Unlock()
		if done {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func waitRuntimeNotifications(r *Runtime, timeout time.Duration, needle string) []string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		notes := r.CollectNotifications()
		if containsText(notes, needle) {
			return notes
		}
		time.Sleep(5 * time.Millisecond)
	}
	return r.CollectNotifications()
}

func writeTestBoardTask(t *testing.T, root string, task *boardTask) {
	t.Helper()
	if err := os.MkdirAll(taskBoardDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskBoardDir(root), task.ID+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsText(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

type teammateTestModel struct {
	usage *schema.TokenUsage
}

func (m *teammateTestModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	msg := schema.AssistantMessage("teammate result", nil)
	if m.usage != nil {
		usage := *m.usage
		msg.ResponseMeta = &schema.ResponseMeta{Usage: &usage}
	}
	return msg, nil
}

func (m *teammateTestModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *teammateTestModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

type routeUsageTeammateTestModel struct {
	usage *schema.TokenUsage
}

func (m *routeUsageTeammateTestModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	collector, callID := modelroute.Record(ctx, modelroute.Simple, "simple-model")
	msg := schema.AssistantMessage("teammate result", nil)
	if m.usage != nil {
		usage := *m.usage
		msg.ResponseMeta = &schema.ResponseMeta{Usage: &usage}
		if collector != nil {
			collector.AddUsage(callID, &usage)
		}
	}
	return msg, nil
}

func (m *routeUsageTeammateTestModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *routeUsageTeammateTestModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

type blockingTeammateTestModel struct {
	started chan struct{}
	release chan struct{}
}

func (m *blockingTeammateTestModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	select {
	case <-m.started:
	default:
		close(m.started)
	}
	select {
	case <-m.release:
	case <-ctx.Done():
	}
	return schema.AssistantMessage("done", nil), nil
}

func (m *blockingTeammateTestModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *blockingTeammateTestModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

type recordingTeammateTestModel struct {
	teammateTestModel
	mu      sync.Mutex
	records []string
}

func (m *recordingTeammateTestModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	var last string
	if len(input) > 0 && input[len(input)-1] != nil {
		last = input[len(input)-1].Content
	}
	m.mu.Lock()
	m.records = append(m.records, last)
	m.mu.Unlock()
	return schema.AssistantMessage("handled: "+last, nil), nil
}

func (m *recordingTeammateTestModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *recordingTeammateTestModel) prompts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.records...)
}

func (m *recordingTeammateTestModel) sawPrompt(needle string) bool {
	for _, prompt := range m.prompts() {
		if strings.Contains(prompt, needle) {
			return true
		}
	}
	return false
}

func denyPrompt(string) (string, bool) {
	return "n", true
}

var _ permission.PromptFunc = denyPrompt
