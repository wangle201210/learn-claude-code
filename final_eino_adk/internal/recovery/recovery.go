package recovery

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/eino/schema"
)

const (
	ControlExtraKey      = "final_eino_recovery_control"
	ContinuationPrompt   = "Output token limit hit. Resume directly - no apology, no recap. Pick up mid-thought."
	MaxContinuationTurns = 3
)

func IsPromptTooLong(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{
		"prompt too long",
		"prompt_too_long",
		"context length",
		"context_length_exceeded",
		"maximum context",
		"max context",
		"too many tokens",
		"input tokens",
		"exceeds context",
		"exceeded token limit",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func IsRetryableModelError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return !IsPromptTooLong(err)
}

func IsOutputTruncated(msg *schema.Message) bool {
	if msg == nil || msg.ResponseMeta == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(msg.ResponseMeta.FinishReason)) {
	case "length", "max_tokens":
		return true
	default:
		return false
	}
}

func ContinuationMessage() *schema.Message {
	msg := schema.UserMessage(ContinuationPrompt)
	msg.Extra = map[string]any{ControlExtraKey: true}
	return msg
}

func IsControlMessage(msg *schema.Message) bool {
	if msg == nil || msg.Extra == nil {
		return false
	}
	_, ok := msg.Extra[ControlExtraKey]
	return ok
}
