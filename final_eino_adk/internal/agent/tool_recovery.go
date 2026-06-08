package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type toolErrorRecoveryMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
}

func newToolErrorRecoveryMiddleware() adk.ChatModelAgentMiddleware {
	return &toolErrorRecoveryMiddleware{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}}
}

func (m *toolErrorRecoveryMiddleware) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
		output, err := endpoint(ctx, argumentsInJSON, opts...)
		if err == nil || shouldPropagateToolError(ctx, err) {
			return output, err
		}
		return formatToolError(tCtx, err), nil
	}, nil
}

func (m *toolErrorRecoveryMiddleware) WrapEnhancedInvokableToolCall(_ context.Context, endpoint adk.EnhancedInvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.EnhancedInvokableToolCallEndpoint, error) {
	return func(ctx context.Context, argument *schema.ToolArgument, opts ...tool.Option) (*schema.ToolResult, error) {
		result, err := endpoint(ctx, argument, opts...)
		if err == nil || shouldPropagateToolError(ctx, err) {
			return result, err
		}
		return &schema.ToolResult{Parts: []schema.ToolOutputPart{{
			Type: schema.ToolPartTypeText,
			Text: formatToolError(tCtx, err),
		}}}, nil
	}, nil
}

func shouldPropagateToolError(ctx context.Context, err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		ctx.Err() != nil
}

func formatToolError(tCtx *adk.ToolContext, err error) string {
	name := "unknown"
	if tCtx != nil && strings.TrimSpace(tCtx.Name) != "" {
		name = strings.TrimSpace(tCtx.Name)
	}
	return fmt.Sprintf("Tool error from %s: %v\nThe agent should inspect the error, adjust the tool arguments, and continue without aborting the run.", name, err)
}
