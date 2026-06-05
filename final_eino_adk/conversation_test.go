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
	history.commit([]*schema.Message{firstUser, firstAssistant})

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
	history.commit([]*schema.Message{firstUser})

	input, _ := history.nextInput("second")
	input[0] = schema.UserMessage("mutated")

	nextInput, _ := history.nextInput("third")
	assertMessage(t, nextInput[0], schema.User, "first")
}

func assertMessage(t *testing.T, msg *schema.Message, role schema.RoleType, content string) {
	t.Helper()
	if msg.Role != role || msg.Content != content {
		t.Fatalf("message = role %q content %q, want role %q content %q", msg.Role, msg.Content, role, content)
	}
}
