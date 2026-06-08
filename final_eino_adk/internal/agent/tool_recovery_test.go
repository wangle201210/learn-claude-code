package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestToolErrorRecoveryConvertsInvokableErrorToOutput(t *testing.T) {
	mw := newToolErrorRecoveryMiddleware()
	wrapped, err := mw.WrapInvokableToolCall(context.Background(), func(context.Context, string, ...tool.Option) (string, error) {
		return "", errors.New("boom")
	}, &adk.ToolContext{Name: "grep", CallID: "call-1"})
	if err != nil {
		t.Fatal(err)
	}

	output, err := wrapped(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("wrapped tool returned error: %v", err)
	}
	if !strings.Contains(output, "Tool error from grep: boom") {
		t.Fatalf("output = %q, want tool error text", output)
	}
}

func TestToolErrorRecoveryPropagatesContextErrors(t *testing.T) {
	mw := newToolErrorRecoveryMiddleware()
	wrapped, err := mw.WrapInvokableToolCall(context.Background(), func(context.Context, string, ...tool.Option) (string, error) {
		return "", context.Canceled
	}, &adk.ToolContext{Name: "grep", CallID: "call-1"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = wrapped(context.Background(), `{}`)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestToolErrorRecoveryConvertsEnhancedErrorToResult(t *testing.T) {
	mw := newToolErrorRecoveryMiddleware()
	wrapped, err := mw.WrapEnhancedInvokableToolCall(context.Background(), func(context.Context, *schema.ToolArgument, ...tool.Option) (*schema.ToolResult, error) {
		return nil, errors.New("enhanced boom")
	}, &adk.ToolContext{Name: "read_file", CallID: "call-2"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := wrapped(context.Background(), &schema.ToolArgument{Text: `{}`})
	if err != nil {
		t.Fatalf("wrapped enhanced tool returned error: %v", err)
	}
	if result == nil || len(result.Parts) != 1 {
		t.Fatalf("result = %#v, want one text part", result)
	}
	if !strings.Contains(result.Parts[0].Text, "Tool error from read_file: enhanced boom") {
		t.Fatalf("result text = %q, want tool error text", result.Parts[0].Text)
	}
}
