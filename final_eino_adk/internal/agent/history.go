package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

type HistoryRecorder func([]adk.Message)

type compactController struct {
	mu       sync.Mutex
	requests int
}

func (c *compactController) Request() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests++
}

func (c *compactController) Consume() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.requests == 0 {
		return false
	}
	c.requests--
	return true
}

type compactMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	controller *compactController
	summarizer interface {
		Summarize(context.Context, *adk.ChatModelAgentState) ([]adk.Message, error)
	}
}

func newCompactMiddleware(controller *compactController, summaryMW adk.ChatModelAgentMiddleware) adk.ChatModelAgentMiddleware {
	summarizer, _ := summaryMW.(interface {
		Summarize(context.Context, *adk.ChatModelAgentState) ([]adk.Message, error)
	})
	return &compactMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		controller:                   controller,
		summarizer:                   summarizer,
	}
}

func (m *compactMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if m.controller == nil || !m.controller.Consume() {
		return ctx, state, nil
	}
	if m.summarizer == nil {
		return ctx, state, nil
	}
	finalMessages, err := m.summarizer.Summarize(ctx, state)
	if err != nil {
		return nil, nil, err
	}
	next := *state
	next.Messages = finalMessages
	fmt.Println("\n\033[90m[compact] history summarized\033[0m")
	return ctx, &next, nil
}

type historyRecorderMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	record HistoryRecorder
}

func newHistoryRecorderMiddleware(record HistoryRecorder) adk.ChatModelAgentMiddleware {
	return &historyRecorderMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		record:                       record,
	}
}

func (m *historyRecorderMiddleware) AfterAgent(ctx context.Context, state *adk.ChatModelAgentState) (context.Context, error) {
	if m.record != nil {
		m.record(copyHistoryMessages(state.Messages))
	}
	return ctx, nil
}

func copyHistoryMessages(messages []adk.Message) []adk.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]adk.Message, 0, len(messages))
	for _, msg := range messages {
		if msg != nil && msg.Extra != nil {
			if _, ok := msg.Extra["final_eino_memory_context"]; ok {
				continue
			}
		}
		out = append(out, cloneMessage(msg))
	}
	return out
}

func cloneMessage(msg adk.Message) adk.Message {
	if msg == nil {
		return nil
	}
	copied := *msg
	if msg.ToolCalls != nil {
		copied.ToolCalls = make([]schema.ToolCall, len(msg.ToolCalls))
		copy(copied.ToolCalls, msg.ToolCalls)
	}
	if msg.Extra != nil {
		copied.Extra = make(map[string]any, len(msg.Extra))
		for k, v := range msg.Extra {
			copied.Extra[k] = v
		}
	}
	return &copied
}
