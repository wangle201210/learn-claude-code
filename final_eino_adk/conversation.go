package main

import (
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

type conversationHistory struct {
	mu       sync.Mutex
	messages []adk.Message
	replaced bool
}

func (h *conversationHistory) nextInput(query string) ([]adk.Message, adk.Message) {
	userMessage := schema.UserMessage(query)
	input := append(h.copyMessages(), userMessage)
	return input, userMessage
}

func (h *conversationHistory) beginRound() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.replaced = false
}

func (h *conversationHistory) replace(messages []adk.Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.messages = copyMessages(messages)
	h.replaced = true
}

func (h *conversationHistory) commitFallback(userMessage adk.Message, roundMessages []adk.Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.replaced {
		h.replaced = false
		return
	}
	if userMessage != nil {
		h.messages = append(h.messages, userMessage)
	}
	h.messages = append(h.messages, roundMessages...)
}

func (h *conversationHistory) copyMessages() []adk.Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	return copyMessages(h.messages)
}

func copyMessages(messages []adk.Message) []adk.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]adk.Message, len(messages))
	for i, msg := range messages {
		out[i] = cloneMessage(msg)
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
