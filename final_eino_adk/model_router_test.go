package main

import (
	"context"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestModelRouterClassifiesSimplePrompt(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{schema.UserMessage("你好，解释一下 defer")})
	if decision.tier != modelRouteSimple {
		t.Fatalf("tier = %s, want simple (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterClassifiesComplexPrompt(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{schema.UserMessage("修复 final_eino_adk 里面上下文压缩和 memory 的异常，并补测试")})
	if decision.tier != modelRouteComplex {
		t.Fatalf("tier = %s, want complex (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterClassifiesToolTrafficAsComplex(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{
		schema.UserMessage("what happened"),
		{Role: schema.Tool, Content: "tool output"},
	})
	if decision.tier != modelRouteComplex {
		t.Fatalf("tier = %s, want complex (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterUsesSimpleModelForSimplePrompt(t *testing.T) {
	base := &captureModel{}
	routed := newRoutedModel(base, modelRouterConfig{
		defaultModel: "complex-model",
		simpleModel:  "simple-model",
		complexModel: "complex-model",
	})

	_, err := routed.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}

	if got := base.lastModel(); got != "simple-model" {
		t.Fatalf("routed model = %q, want simple-model", got)
	}
}

func TestModelRouterLeavesComplexDefaultUnchanged(t *testing.T) {
	base := &captureModel{}
	routed := newRoutedModel(base, modelRouterConfig{
		defaultModel: "complex-model",
		simpleModel:  "simple-model",
		complexModel: "complex-model",
	})

	_, err := routed.Generate(context.Background(), []*schema.Message{schema.UserMessage("implement a fix in foo.go")})
	if err != nil {
		t.Fatal(err)
	}

	if got := base.lastModel(); got != "" {
		t.Fatalf("routed model override = %q, want none for default complex model", got)
	}
}

func TestModelRouterRespectsExplicitModelOption(t *testing.T) {
	base := &captureModel{}
	routed := newRoutedModel(base, modelRouterConfig{
		defaultModel: "complex-model",
		simpleModel:  "simple-model",
		complexModel: "complex-model",
	})

	_, err := routed.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")}, model.WithModel("manual-model"))
	if err != nil {
		t.Fatal(err)
	}

	if got := base.lastModel(); got != "manual-model" {
		t.Fatalf("routed model = %q, want manual-model", got)
	}
}

func TestModelRouterWithToolsKeepsRouting(t *testing.T) {
	base := &captureModel{}
	routed, err := newRoutedModel(base, modelRouterConfig{
		defaultModel: "complex-model",
		simpleModel:  "simple-model",
		complexModel: "complex-model",
	}).WithTools([]*schema.ToolInfo{{Name: "read_file"}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = routed.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}

	if got := base.lastModel(); got != "simple-model" {
		t.Fatalf("routed model = %q, want simple-model", got)
	}
}

func TestModelRouterDisabledWithoutSimpleModel(t *testing.T) {
	base := &captureModel{}
	routed := newRoutedModel(base, modelRouterConfig{
		defaultModel: "complex-model",
		complexModel: "complex-model",
	})

	if routed != base {
		t.Fatal("router should not wrap without simple model")
	}
}

type captureModel struct {
	mu     sync.Mutex
	models []string
}

func (m *captureModel) Generate(_ context.Context, _ []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.capture(opts...)
	return schema.AssistantMessage("ok", nil), nil
}

func (m *captureModel) Stream(_ context.Context, _ []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.capture(opts...)
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("ok", nil)}), nil
}

func (m *captureModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *captureModel) capture(opts ...model.Option) {
	common := model.GetCommonOptions(nil, opts...)
	selected := ""
	if common.Model != nil {
		selected = *common.Model
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.models = append(m.models, selected)
}

func (m *captureModel) lastModel() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.models) == 0 {
		return ""
	}
	return m.models[len(m.models)-1]
}
