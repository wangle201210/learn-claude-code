package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestBeforeModelRewriteStateLoadsMemoryWithoutModelCall(t *testing.T) {
	withTempMemoryDir(t)
	writeMemoryFile("project", "project", "project facts", "The repo uses Eino ADK.")
	fake := &countingModel{}
	mw := NewMiddleware(fake)

	_, next, err := mw.BeforeModelRewriteState(context.Background(), &adk.ChatModelAgentState{
		Messages: []*schema.Message{schema.UserMessage("tell me about the project")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if fake.calls() != 0 {
		t.Fatalf("memory load model calls = %d, want 0", fake.calls())
	}
	if len(next.Messages) != 2 {
		t.Fatalf("message count = %d, want memory + user", len(next.Messages))
	}
	if !hasMemoryExtra(next.Messages[0]) {
		t.Fatalf("first message should be memory context: %#v", next.Messages[0])
	}
}

func TestAfterAgentSkipsExtractionForOrdinaryConversation(t *testing.T) {
	withTempMemoryDir(t)
	fake := &countingModel{}
	mw := NewMiddleware(fake)

	_, err := mw.AfterAgent(context.Background(), &adk.ChatModelAgentState{
		Messages: []*schema.Message{
			schema.UserMessage("hello"),
			schema.AssistantMessage("hi", nil),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)

	if fake.calls() != 0 {
		t.Fatalf("ordinary conversation memory extraction calls = %d, want 0", fake.calls())
	}
}

func TestAfterAgentSchedulesExtractionAsynchronously(t *testing.T) {
	withTempMemoryDir(t)
	fake := &countingModel{block: make(chan struct{})}
	mw := NewMiddleware(fake)

	start := time.Now()
	_, err := mw.AfterAgent(context.Background(), &adk.ChatModelAgentState{
		Messages: []*schema.Message{
			schema.UserMessage("请记住我以后希望回答更简洁"),
			schema.AssistantMessage("好的", nil),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("AfterAgent blocked for %s, want async scheduling", elapsed)
	}

	if !fake.waitCalls(1, time.Second) {
		t.Fatal("memory extraction was not scheduled")
	}
	close(fake.block)
}

func TestShouldExtractMemoriesRequiresTrigger(t *testing.T) {
	if shouldExtractMemories([]*schema.Message{schema.UserMessage("normal request")}) {
		t.Fatal("normal request should not trigger memory extraction")
	}
	if !shouldExtractMemories([]*schema.Message{schema.UserMessage("remember my preference")}) {
		t.Fatal("explicit memory request should trigger extraction")
	}
}

func withTempMemoryDir(t *testing.T) {
	t.Helper()
	oldDir, oldIndex := memoryDir, memoryIndex
	t.Cleanup(func() {
		memoryDir = oldDir
		memoryIndex = oldIndex
		invalidateMemoryCache()
	})
	memoryDir = t.TempDir()
	memoryIndex = memoryDir + "/MEMORY.md"
	invalidateMemoryCache()
}

func hasMemoryExtra(msg *schema.Message) bool {
	if msg == nil || msg.Extra == nil {
		return false
	}
	_, ok := msg.Extra["final_eino_memory_context"]
	return ok
}

type countingModel struct {
	mu    sync.Mutex
	n     int
	block chan struct{}
}

func (m *countingModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	m.n++
	m.mu.Unlock()
	if m.block != nil {
		<-m.block
	}
	return schema.AssistantMessage("[]", nil), nil
}

func (m *countingModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.mu.Lock()
	m.n++
	m.mu.Unlock()
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("[]", nil)}), nil
}

func (m *countingModel) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.n
}

func (m *countingModel) waitCalls(want int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m.calls() >= want {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
