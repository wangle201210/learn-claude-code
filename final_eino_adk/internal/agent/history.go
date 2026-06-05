package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

type HistoryRecorder func([]adk.Message)

const compactControlExtraKey = "final_eino_compact_control"

type CompactController struct {
	mu       sync.Mutex
	requests []compactRequest
}

type compactRequest struct {
	afterSummaryPrompt string
}

func NewCompactController() *CompactController {
	return &CompactController{}
}

func (c *CompactController) Request() {
	c.RequestWithPrompt("")
}

func (c *CompactController) RequestWithPrompt(afterSummaryPrompt string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, compactRequest{afterSummaryPrompt: afterSummaryPrompt})
}

func (c *CompactController) consume() (compactRequest, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		return compactRequest{}, false
	}
	req := c.requests[0]
	c.requests = c.requests[1:]
	return req, true
}

type compactMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	controller *CompactController
	summarizer interface {
		Summarize(context.Context, *adk.ChatModelAgentState) ([]adk.Message, error)
	}
}

func newCompactMiddleware(controller *CompactController, summaryMW adk.ChatModelAgentMiddleware) adk.ChatModelAgentMiddleware {
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
	if m.controller == nil {
		return ctx, state, nil
	}
	request, ok := m.controller.consume()
	if !ok {
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
	if request.afterSummaryPrompt != "" {
		next.Messages = append(next.Messages, compactControlMessage(request.afterSummaryPrompt))
	}
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
	skipCompactConfirmation := false
	for _, msg := range messages {
		if hasMessageExtra(msg, "final_eino_memory_context") {
			continue
		}
		if hasMessageExtra(msg, compactControlExtraKey) {
			skipCompactConfirmation = true
			continue
		}
		if skipCompactConfirmation {
			skipCompactConfirmation = false
			if msg != nil && msg.Role == schema.Assistant && len(msg.ToolCalls) == 0 {
				continue
			}
		}
		out = append(out, cloneMessage(msg))
	}
	return out
}

func compactControlMessage(content string) adk.Message {
	msg := schema.UserMessage(content)
	msg.Extra = map[string]any{compactControlExtraKey: true}
	return msg
}

func hasMessageExtra(msg adk.Message, key string) bool {
	if msg == nil || msg.Extra == nil {
		return false
	}
	_, ok := msg.Extra[key]
	return ok
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
