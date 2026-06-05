package modelroute

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type Tier string

const (
	Simple   Tier = "simple"
	Standard Tier = "standard"
	Complex  Tier = "complex"
)

type contextKey struct{}

func WithTier(ctx context.Context, tier Tier) context.Context {
	if !validTier(tier) {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, tier)
}

func TierFromContext(ctx context.Context) (Tier, bool) {
	tier, ok := ctx.Value(contextKey{}).(Tier)
	if !ok || !validTier(tier) {
		return "", false
	}
	return tier, true
}

func WrapBase(base model.BaseChatModel, tier Tier) model.BaseChatModel {
	if base == nil || !validTier(tier) {
		return base
	}
	return &hintedBaseModel{base: base, tier: tier}
}

func WrapToolCalling(base model.ToolCallingChatModel, tier Tier) model.ToolCallingChatModel {
	if base == nil || !validTier(tier) {
		return base
	}
	return &hintedToolCallingModel{base: base, tier: tier}
}

type hintedBaseModel struct {
	base model.BaseChatModel
	tier Tier
}

func (m *hintedBaseModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return m.base.Generate(WithTier(ctx, m.tier), input, opts...)
}

func (m *hintedBaseModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.base.Stream(WithTier(ctx, m.tier), input, opts...)
}

type hintedToolCallingModel struct {
	base model.ToolCallingChatModel
	tier Tier
}

func (m *hintedToolCallingModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return m.base.Generate(WithTier(ctx, m.tier), input, opts...)
}

func (m *hintedToolCallingModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.base.Stream(WithTier(ctx, m.tier), input, opts...)
}

func (m *hintedToolCallingModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	withTools, err := m.base.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return WrapToolCalling(withTools, m.tier), nil
}

func validTier(tier Tier) bool {
	switch tier {
	case Simple, Standard, Complex:
		return true
	default:
		return false
	}
}
