package agent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestCompactControllerConsumesOneRequest(t *testing.T) {
	controller := &compactController{}
	if controller.Consume() {
		t.Fatal("empty controller consumed a request")
	}

	controller.Request()
	if !controller.Consume() {
		t.Fatal("controller did not consume queued request")
	}
	if controller.Consume() {
		t.Fatal("controller consumed request twice")
	}
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

func memoryContextMessage(content string) adk.Message {
	msg := schema.UserMessage(content)
	msg.Extra = map[string]any{"final_eino_memory_context": true}
	return msg
}
