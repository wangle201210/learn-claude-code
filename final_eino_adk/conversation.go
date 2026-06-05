package main

import (
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

type conversationHistory struct {
	messages []adk.Message
}

func (h *conversationHistory) nextInput(query string) ([]adk.Message, adk.Message) {
	userMessage := schema.UserMessage(query)
	input := append(h.copyMessages(), userMessage)
	return input, userMessage
}

func (h *conversationHistory) commit(roundMessages []adk.Message) {
	h.messages = append(h.messages, roundMessages...)
}

func (h *conversationHistory) copyMessages() []adk.Message {
	if len(h.messages) == 0 {
		return nil
	}
	out := make([]adk.Message, len(h.messages))
	copy(out, h.messages)
	return out
}
