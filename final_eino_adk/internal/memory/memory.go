package memory

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/workspace"
)

const memoryExtractionCooldown = 30 * time.Second

var (
	memoryDir   = filepath.Join(workspace.Dir(), ".memory")
	memoryIndex = filepath.Join(memoryDir, "MEMORY.md")
)

type memoryMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	model     model.BaseChatModel
	extractor asyncExtractor
}

func NewMiddleware(m model.BaseChatModel) adk.ChatModelAgentMiddleware {
	return &memoryMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		model:                        m,
		extractor:                    asyncExtractor{cooldown: memoryExtractionCooldown},
	}
}

func (m *memoryMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	state = withoutMemoryMessages(state)
	content := loadMemories(state.Messages)
	if content == "" {
		return ctx, state, nil
	}
	nState := *state
	nState.Messages = insertMemoryMessage(nState.Messages, content)
	return ctx, &nState, nil
}

func (m *memoryMiddleware) AfterAgent(ctx context.Context, state *adk.ChatModelAgentState) (context.Context, error) {
	cleanState := withoutMemoryMessages(state)
	m.extractor.schedule(ctx, m.model, cleanState.Messages)
	return ctx, nil
}

type asyncExtractor struct {
	mu       sync.Mutex
	running  bool
	lastRun  time.Time
	cooldown time.Duration
}

func (e *asyncExtractor) schedule(ctx context.Context, m model.BaseChatModel, messages []*schema.Message) {
	if m == nil || !shouldExtractMemories(messages) {
		return
	}
	copied := cloneMessages(messages)

	e.mu.Lock()
	if e.running || time.Since(e.lastRun) < e.cooldown {
		e.mu.Unlock()
		return
	}
	e.running = true
	e.lastRun = time.Now()
	e.mu.Unlock()

	go func() {
		defer func() {
			e.mu.Lock()
			e.running = false
			e.mu.Unlock()
		}()
		workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
		defer cancel()
		extractMemories(workCtx, m, copied)
		consolidateMemories(workCtx, m)
		warmMemorySnapshot()
	}()
}

func withoutMemoryMessages(state *adk.ChatModelAgentState) *adk.ChatModelAgentState {
	var filtered []*schema.Message
	changed := false
	for _, msg := range state.Messages {
		if msg.Extra != nil {
			if _, ok := msg.Extra["final_eino_memory_context"]; ok {
				changed = true
				continue
			}
		}
		filtered = append(filtered, msg)
	}
	if !changed {
		return state
	}
	nState := *state
	nState.Messages = filtered
	return &nState
}

func insertMemoryMessage(messages []*schema.Message, content string) []*schema.Message {
	msg := schema.UserMessage(content)
	msg.Extra = map[string]any{"final_eino_memory_context": true}

	out := make([]*schema.Message, 0, len(messages)+1)
	inserted := false
	for _, existing := range messages {
		if !inserted && existing.Role == schema.User {
			out = append(out, msg)
			inserted = true
		}
		out = append(out, existing)
	}
	if !inserted {
		out = append(out, msg)
	}
	return out
}

func cloneMessages(messages []*schema.Message) []*schema.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]*schema.Message, len(messages))
	for i, msg := range messages {
		if msg == nil {
			continue
		}
		copied := *msg
		if msg.ToolCalls != nil {
			copied.ToolCalls = append([]schema.ToolCall(nil), msg.ToolCalls...)
		}
		if msg.Extra != nil {
			copied.Extra = make(map[string]any, len(msg.Extra))
			for k, v := range msg.Extra {
				copied.Extra[k] = v
			}
		}
		out[i] = &copied
	}
	return out
}
