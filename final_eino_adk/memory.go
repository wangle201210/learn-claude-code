package main

import (
	"context"
	"path/filepath"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

var (
	memoryDir   = filepath.Join(workdir, ".memory")
	memoryIndex = filepath.Join(memoryDir, "MEMORY.md")
)

type memoryMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	model model.BaseChatModel
}

func newMemoryMiddleware(m model.BaseChatModel) adk.ChatModelAgentMiddleware {
	return &memoryMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		model:                        m,
	}
}

func (m *memoryMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	state = withoutMemoryMessages(state)
	content := loadMemories(ctx, m.model, state.Messages)
	if content == "" {
		return ctx, state, nil
	}
	nState := *state
	nState.Messages = insertMemoryMessage(nState.Messages, content)
	return ctx, &nState, nil
}

func (m *memoryMiddleware) AfterAgent(ctx context.Context, state *adk.ChatModelAgentState) (context.Context, error) {
	cleanState := withoutMemoryMessages(state)
	extractMemories(ctx, m.model, cleanState.Messages)
	consolidateMemories(ctx, m.model)
	return ctx, nil
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
