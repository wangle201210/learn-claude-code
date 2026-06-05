package agent

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestDynamicToolSearchMiddlewareDefersMCPTools(t *testing.T) {
	ctx := context.Background()
	mw, err := dynamicToolSearchMiddleware(ctx, []tool.BaseTool{
		namedTestTool{name: "mcp__docs__search", desc: "Search docs."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if mw == nil {
		t.Fatal("middleware is nil")
	}

	_, runCtx, err := mw.BeforeAgent(ctx, &adk.ChatModelAgentContext{
		Tools: []tool.BaseTool{namedTestTool{name: "static_tool", desc: "Static tool."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	infos := toolInfos(t, ctx, runCtx.Tools)
	if !slices.Contains(toolInfoNames(infos), "tool_search") {
		t.Fatalf("run tools = %v, want tool_search", toolInfoNames(infos))
	}

	_, state, err := mw.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{
		Messages:  []*schema.Message{schema.UserMessage("hello")},
		ToolInfos: infos,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := toolInfoNames(state.ToolInfos)
	if !slices.Contains(names, "static_tool") {
		t.Fatalf("visible tools = %v, want static_tool", names)
	}
	if !slices.Contains(names, "tool_search") {
		t.Fatalf("visible tools = %v, want tool_search", names)
	}
	if slices.Contains(names, "mcp__docs__search") {
		t.Fatalf("visible tools = %v, want MCP tool deferred", names)
	}
	if len(state.Messages) != 2 {
		t.Fatalf("messages = %d, want reminder + user", len(state.Messages))
	}
	if !strings.Contains(state.Messages[0].Content, "<available-deferred-tools>") ||
		!strings.Contains(state.Messages[0].Content, "mcp__docs__search") {
		t.Fatalf("reminder content = %q", state.Messages[0].Content)
	}
	if !hasMessageExtra(state.Messages[0], toolSearchExtraKey) {
		t.Fatalf("reminder extra = %#v, want %s", state.Messages[0].Extra, toolSearchExtraKey)
	}
}

func TestDynamicToolSearchMiddlewareEmptyTools(t *testing.T) {
	mw, err := dynamicToolSearchMiddleware(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if mw != nil {
		t.Fatal("empty dynamic tools should not create middleware")
	}
}

type namedTestTool struct {
	name string
	desc string
}

func (t namedTestTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: t.desc}, nil
}

func toolInfos(t *testing.T, ctx context.Context, tools []tool.BaseTool) []*schema.ToolInfo {
	t.Helper()
	out := make([]*schema.ToolInfo, 0, len(tools))
	for _, tl := range tools {
		info, err := tl.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, info)
	}
	return out
}

func toolInfoNames(infos []*schema.ToolInfo) []string {
	out := make([]string, 0, len(infos))
	for _, info := range infos {
		if info != nil {
			out = append(out, info.Name)
		}
	}
	return out
}
