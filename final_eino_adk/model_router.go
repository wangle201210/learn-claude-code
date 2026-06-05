package main

import (
	"context"
	"os"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/modelroute"
)

type modelRouteTier string

const (
	modelRouteSimple   modelRouteTier = "simple"
	modelRouteStandard modelRouteTier = "standard"
	modelRouteComplex  modelRouteTier = "complex"
)

type modelRouteDecision struct {
	tier   modelRouteTier
	reason string
}

type modelRouterConfig struct {
	defaultModel  string
	simpleModel   string
	standardModel string
	complexModel  string
	disabled      bool
}

type routedModel struct {
	base model.ToolCallingChatModel
	cfg  modelRouterConfig
}

func newRoutedModel(base model.ToolCallingChatModel, cfg modelRouterConfig) model.ToolCallingChatModel {
	if base == nil || !cfg.enabled() {
		return base
	}
	return &routedModel{base: base, cfg: cfg}
}

func modelRouterConfigFromEnv() modelRouterConfig {
	return modelRouterConfig{
		defaultModel: os.Getenv("OPENAI_MODEL"),
		simpleModel: firstNonEmptyEnv(
			"FINAL_EINO_SIMPLE_MODEL",
			"OPENAI_SIMPLE_MODEL",
			"ANTHROPIC_SMALL_FAST_MODEL",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL",
		),
		standardModel: firstNonEmptyEnv(
			"FINAL_EINO_STANDARD_MODEL",
			"OPENAI_STANDARD_MODEL",
			"ANTHROPIC_DEFAULT_SONNET_MODEL",
			"OPENAI_MODEL",
		),
		complexModel: firstNonEmptyEnv(
			"FINAL_EINO_COMPLEX_MODEL",
			"OPENAI_COMPLEX_MODEL",
			"ANTHROPIC_DEFAULT_OPUS_MODEL",
			"OPENAI_MODEL",
		),
		disabled: isEnvFalse(os.Getenv("FINAL_EINO_MODEL_ROUTING")),
	}
}

func modelRoutingSummaryFromEnv() string {
	cfg := modelRouterConfigFromEnv()
	if !cfg.enabled() {
		return ""
	}
	return "model route 已启用：simple=" + cfg.modelForTier(modelRouteSimple) +
		"，standard=" + cfg.modelForTier(modelRouteStandard) +
		"，complex=" + cfg.modelForTier(modelRouteComplex)
}

func (c modelRouterConfig) enabled() bool {
	if c.disabled {
		return false
	}
	for _, selected := range []string{
		c.modelForTier(modelRouteSimple),
		c.modelForTier(modelRouteStandard),
		c.modelForTier(modelRouteComplex),
	} {
		if selected != "" && selected != c.defaultModel {
			return true
		}
	}
	return false
}

func (c modelRouterConfig) modelForTier(tier modelRouteTier) string {
	switch tier {
	case modelRouteSimple:
		if c.simpleModel != "" {
			return c.simpleModel
		}
		return c.modelForTier(modelRouteStandard)
	case modelRouteStandard:
		if c.standardModel != "" {
			return c.standardModel
		}
		return c.defaultModel
	case modelRouteComplex:
		if c.complexModel != "" {
			return c.complexModel
		}
		return c.modelForTier(modelRouteStandard)
	default:
		return c.defaultModel
	}
}

func (m *routedModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	route := m.route(ctx, input, opts...)
	msg, err := m.base.Generate(ctx, input, route.opts...)
	if err == nil {
		route.recordUsage(msg)
	}
	return msg, err
}

func (m *routedModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	route := m.route(ctx, input, opts...)
	reader, err := m.base.Stream(ctx, input, route.opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderWithConvert(reader, func(msg *schema.Message) (*schema.Message, error) {
		route.recordUsage(msg)
		return msg, nil
	}), nil
}

func (m *routedModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	withTools, err := m.base.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return newRoutedModel(withTools, m.cfg), nil
}

type modelRouteCall struct {
	usage  *modelroute.UsageCollector
	callID int
	opts   []model.Option
}

func (c modelRouteCall) recordUsage(msg *schema.Message) {
	if c.usage == nil || msg == nil || msg.ResponseMeta == nil || msg.ResponseMeta.Usage == nil {
		return
	}
	c.usage.AddUsage(c.callID, msg.ResponseMeta.Usage)
}

func (m *routedModel) route(ctx context.Context, input []*schema.Message, opts ...model.Option) modelRouteCall {
	if !m.cfg.enabled() {
		return modelRouteCall{opts: opts}
	}
	if explicit := model.GetCommonOptions(nil, opts...).Model; explicit != nil {
		collector, callID := modelroute.Record(ctx, modelroute.Explicit, *explicit)
		return modelRouteCall{usage: collector, callID: callID, opts: opts}
	}

	decision := routeDecision(ctx, input)
	selected := m.cfg.modelForTier(decision.tier)
	collector, callID := modelroute.Record(ctx, toModelRouteTier(decision.tier), selected)
	if selected == "" || selected == m.cfg.defaultModel {
		return modelRouteCall{usage: collector, callID: callID, opts: opts}
	}
	out := make([]model.Option, 0, len(opts)+1)
	out = append(out, opts...)
	out = append(out, model.WithModel(selected))
	return modelRouteCall{usage: collector, callID: callID, opts: out}
}

func routeDecision(ctx context.Context, input []*schema.Message) modelRouteDecision {
	if tier, ok := modelroute.TierFromContext(ctx); ok {
		switch tier {
		case modelroute.Simple:
			return modelRouteDecision{tier: modelRouteSimple, reason: "context hint"}
		case modelroute.Standard:
			return modelRouteDecision{tier: modelRouteStandard, reason: "context hint"}
		case modelroute.Complex:
			return modelRouteDecision{tier: modelRouteComplex, reason: "context hint"}
		}
	}
	return classifyModelRoute(input)
}

func toModelRouteTier(tier modelRouteTier) modelroute.Tier {
	switch tier {
	case modelRouteSimple:
		return modelroute.Simple
	case modelRouteStandard:
		return modelroute.Standard
	case modelRouteComplex:
		return modelroute.Complex
	default:
		return ""
	}
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func isEnvFalse(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "no", "off", "disabled":
		return true
	default:
		return false
	}
}
