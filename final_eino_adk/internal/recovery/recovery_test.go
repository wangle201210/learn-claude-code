package recovery

import (
	"errors"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestIsPromptTooLong(t *testing.T) {
	for _, text := range []string{
		"context_length_exceeded",
		"prompt too long",
		"too many tokens in request",
		"maximum context length exceeded",
	} {
		if !IsPromptTooLong(errors.New(text)) {
			t.Fatalf("%q should be prompt-too-long", text)
		}
	}
	if IsPromptTooLong(errors.New("rate limit")) {
		t.Fatal("rate limit should not be prompt-too-long")
	}
}

func TestIsOutputTruncated(t *testing.T) {
	msg := schema.AssistantMessage("partial", nil)
	msg.ResponseMeta = &schema.ResponseMeta{FinishReason: "max_tokens"}
	if !IsOutputTruncated(msg) {
		t.Fatal("max_tokens should be treated as truncated")
	}
	msg.ResponseMeta.FinishReason = "stop"
	if IsOutputTruncated(msg) {
		t.Fatal("stop should not be treated as truncated")
	}
}

func TestContinuationMessageIsControl(t *testing.T) {
	msg := ContinuationMessage()
	if !IsControlMessage(msg) {
		t.Fatal("continuation message should be marked as recovery control")
	}
}
