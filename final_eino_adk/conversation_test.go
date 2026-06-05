package main

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestConversationHistoryCarriesPreviousRound(t *testing.T) {
	history := &conversationHistory{}

	firstInput, firstUser := history.nextInput("first")
	if len(firstInput) != 1 || firstInput[0].Content != "first" {
		t.Fatalf("first input = %#v, want only first user message", firstInput)
	}

	firstAssistant := schema.AssistantMessage("first answer", nil)
	history.commitFallback(firstUser, []*schema.Message{firstAssistant})

	secondInput, secondUser := history.nextInput("second")
	if len(secondInput) != 3 {
		t.Fatalf("second input length = %d, want 3", len(secondInput))
	}

	assertMessage(t, secondInput[0], schema.User, "first")
	assertMessage(t, secondInput[1], schema.Assistant, "first answer")
	assertMessage(t, secondInput[2], schema.User, "second")

	if secondInput[2] != secondUser {
		t.Fatal("nextInput did not return the user message appended to input")
	}
}

func TestConversationHistoryReturnsCopy(t *testing.T) {
	history := &conversationHistory{}
	_, firstUser := history.nextInput("first")
	history.commitFallback(firstUser, nil)

	input, _ := history.nextInput("second")
	input[0] = schema.UserMessage("mutated")

	nextInput, _ := history.nextInput("third")
	assertMessage(t, nextInput[0], schema.User, "first")
}

func TestConversationHistoryReplaceSkipsFallbackCommit(t *testing.T) {
	history := &conversationHistory{}
	history.beginRound()
	_, user := history.nextInput("first")
	history.replace([]*schema.Message{
		schema.UserMessage("[Compacted]\nsummary"),
		schema.AssistantMessage("answer", nil),
	})
	history.commitFallback(user, []*schema.Message{schema.AssistantMessage("fallback answer", nil)})

	nextInput, _ := history.nextInput("second")
	if len(nextInput) != 3 {
		t.Fatalf("next input length = %d, want compacted history + second user", len(nextInput))
	}
	assertMessage(t, nextInput[0], schema.User, "[Compacted]\nsummary")
	assertMessage(t, nextInput[1], schema.Assistant, "answer")
	assertMessage(t, nextInput[2], schema.User, "second")
}

func assertMessage(t *testing.T, msg *schema.Message, role schema.RoleType, content string) {
	t.Helper()
	if msg.Role != role || msg.Content != content {
		t.Fatalf("message = role %q content %q, want role %q content %q", msg.Role, msg.Content, role, content)
	}
}
