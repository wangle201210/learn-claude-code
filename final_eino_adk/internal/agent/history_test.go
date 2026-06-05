package agent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestCompactControllerConsumesOneRequest(t *testing.T) {
	controller := NewCompactController()
	if _, ok := controller.consume(); ok {
		t.Fatal("empty controller consumed a request")
	}

	controller.Request()
	if _, ok := controller.consume(); !ok {
		t.Fatal("controller did not consume queued request")
	}
	if _, ok := controller.consume(); ok {
		t.Fatal("controller consumed request twice")
	}
}

func TestCompactControllerCarriesPrompt(t *testing.T) {
	controller := NewCompactController()
	controller.RequestWithPrompt("done")

	req, ok := controller.consume()
	if !ok {
		t.Fatal("controller did not consume queued request")
	}
	if req.afterSummaryPrompt != "done" {
		t.Fatalf("afterSummaryPrompt = %q, want done", req.afterSummaryPrompt)
	}
}

func TestCompactMiddlewareSummarizesAndAppendsPrompt(t *testing.T) {
	controller := NewCompactController()
	controller.RequestWithPrompt("compact done")
	summarizer := &fakeSummaryMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		summary:                      []adk.Message{schema.UserMessage("[summary]")},
	}
	mw := newCompactMiddleware(controller, summarizer)

	_, next, err := mw.BeforeModelRewriteState(context.Background(), &adk.ChatModelAgentState{
		Messages: []adk.Message{
			schema.UserMessage("first"),
			schema.AssistantMessage("answer", nil),
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !summarizer.called {
		t.Fatal("summarizer was not called")
	}
	if len(next.Messages) != 2 {
		t.Fatalf("next message length = %d, want summary + prompt", len(next.Messages))
	}
	assertAgentMessage(t, next.Messages[0], schema.User, "[summary]")
	assertAgentMessage(t, next.Messages[1], schema.User, "compact done")
}

func TestHistoryRecorderFiltersMemoryContext(t *testing.T) {
	var recorded []adk.Message
	mw := newHistoryRecorderMiddleware(func(messages []adk.Message) {
		recorded = messages
	})

	_, err := mw.AfterAgent(context.Background(), &adk.ChatModelAgentState{
		Messages: []adk.Message{
			schema.UserMessage("keep"),
			memoryContextMessage("drop"),
			schema.AssistantMessage("answer", nil),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(recorded) != 2 {
		t.Fatalf("recorded %d messages, want 2", len(recorded))
	}
	if recorded[0].Content != "keep" || recorded[1].Content != "answer" {
		t.Fatalf("recorded messages = %#v", recorded)
	}
}

func TestHistoryRecorderFiltersSystemMessages(t *testing.T) {
	var recorded []adk.Message
	mw := newHistoryRecorderMiddleware(func(messages []adk.Message) {
		recorded = messages
	})

	_, err := mw.AfterAgent(context.Background(), &adk.ChatModelAgentState{
		Messages: []adk.Message{
			schema.SystemMessage("agent instruction"),
			schema.UserMessage("keep"),
			schema.AssistantMessage("answer", nil),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(recorded) != 2 {
		t.Fatalf("recorded %d messages, want user + assistant", len(recorded))
	}
	if recorded[0].Role != schema.User || recorded[1].Role != schema.Assistant {
		t.Fatalf("recorded messages = %#v", recorded)
	}
}

func TestHistoryRecorderFiltersCompactConfirmation(t *testing.T) {
	var recorded []adk.Message
	mw := newHistoryRecorderMiddleware(func(messages []adk.Message) {
		recorded = messages
	})

	_, err := mw.AfterAgent(context.Background(), &adk.ChatModelAgentState{
		Messages: []adk.Message{
			schema.UserMessage("[summary]"),
			compactControlMessage("confirm"),
			schema.AssistantMessage("compacted", nil),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(recorded) != 1 {
		t.Fatalf("recorded %d messages, want summary only", len(recorded))
	}
	if recorded[0].Content != "[summary]" {
		t.Fatalf("recorded messages = %#v", recorded)
	}
}

func memoryContextMessage(content string) adk.Message {
	msg := schema.UserMessage(content)
	msg.Extra = map[string]any{"final_eino_memory_context": true}
	return msg
}

type fakeSummaryMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	summary []adk.Message
	called  bool
}

func (m *fakeSummaryMiddleware) Summarize(context.Context, *adk.ChatModelAgentState) ([]adk.Message, error) {
	m.called = true
	return m.summary, nil
}

func assertAgentMessage(t *testing.T, msg adk.Message, role schema.RoleType, content string) {
	t.Helper()
	if msg == nil {
		t.Fatal("message is nil")
	}
	if msg.Role != role || msg.Content != content {
		t.Fatalf("message = role %q content %q, want role %q content %q", msg.Role, msg.Content, role, content)
	}
}
