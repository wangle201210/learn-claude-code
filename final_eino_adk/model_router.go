package main

import (
	"context"
	"os"
	"strings"
	"unicode"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type modelRouteTier string

const (
	modelRouteSimple  modelRouteTier = "simple"
	modelRouteComplex modelRouteTier = "complex"
)

type modelRouteDecision struct {
	tier   modelRouteTier
	reason string
}

type modelRouterConfig struct {
	defaultModel string
	simpleModel  string
	complexModel string
	disabled     bool
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
		),
		complexModel: firstNonEmptyEnv(
			"FINAL_EINO_COMPLEX_MODEL",
			"OPENAI_COMPLEX_MODEL",
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
	return "model route 已启用：simple=" + cfg.simpleModel + "，complex=" + cfg.complexModel
}

func (c modelRouterConfig) enabled() bool {
	return !c.disabled && c.simpleModel != "" && c.complexModel != "" && c.simpleModel != c.complexModel
}

func (m *routedModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return m.base.Generate(ctx, input, m.routeOptions(input, opts...)...)
}

func (m *routedModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.base.Stream(ctx, input, m.routeOptions(input, opts...)...)
}

func (m *routedModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	withTools, err := m.base.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return newRoutedModel(withTools, m.cfg), nil
}

func (m *routedModel) routeOptions(input []*schema.Message, opts ...model.Option) []model.Option {
	if !m.cfg.enabled() {
		return opts
	}
	if model.GetCommonOptions(nil, opts...).Model != nil {
		return opts
	}

	selected := m.cfg.complexModel
	if classifyModelRoute(input).tier == modelRouteSimple {
		selected = m.cfg.simpleModel
	}
	if selected == "" || selected == m.cfg.defaultModel {
		return opts
	}
	out := make([]model.Option, 0, len(opts)+1)
	out = append(out, opts...)
	out = append(out, model.WithModel(selected))
	return out
}

func classifyModelRoute(messages []*schema.Message) modelRouteDecision {
	stats := collectRouteStats(messages)
	if stats.hasToolTraffic {
		return modelRouteDecision{tier: modelRouteComplex, reason: "tool traffic"}
	}
	if stats.totalChars > 8000 || stats.messageCount > 16 {
		return modelRouteDecision{tier: modelRouteComplex, reason: "large context"}
	}
	if isComplexPrompt(stats.latestUser) {
		return modelRouteDecision{tier: modelRouteComplex, reason: "complex prompt"}
	}
	return modelRouteDecision{tier: modelRouteSimple, reason: "short prompt"}
}

type routeStats struct {
	latestUser     string
	totalChars     int
	messageCount   int
	hasToolTraffic bool
}

func collectRouteStats(messages []*schema.Message) routeStats {
	var stats routeStats
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		stats.messageCount++
		stats.totalChars += len(msg.Content)
		if msg.Role == schema.Tool || len(msg.ToolCalls) > 0 {
			stats.hasToolTraffic = true
		}
		if msg.Role == schema.User && !isTransientContextMessage(msg) {
			content := strings.TrimSpace(msg.Content)
			if content != "" {
				stats.latestUser = content
			}
		}
	}
	return stats
}

func isTransientContextMessage(msg *schema.Message) bool {
	if msg == nil || msg.Extra == nil {
		return false
	}
	if _, ok := msg.Extra["final_eino_memory_context"]; ok {
		return true
	}
	if _, ok := msg.Extra["__agentsmd_content__"]; ok {
		return true
	}
	return false
}

func isComplexPrompt(prompt string) bool {
	text := strings.ToLower(strings.TrimSpace(prompt))
	if text == "" {
		return false
	}
	if len([]rune(text)) > 500 {
		return true
	}
	for _, marker := range []string{"```", "panic:", "traceback", "exception", "node runerror", "error:", "failed"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	for _, keyword := range complexPromptKeywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	if looksLikeCodeOrPath(text) {
		return true
	}
	return false
}

var complexPromptKeywords = []string{
	"implement",
	"refactor",
	"debug",
	"fix",
	"failing",
	"failure",
	"test",
	"permission",
	"memory",
	"compact",
	"summarize",
	"summary",
	"context",
	"mcp",
	"worktree",
	"push",
	"commit",
	"修改",
	"实现",
	"补充",
	"完善",
	"修复",
	"调试",
	"报错",
	"异常",
	"测试",
	"重构",
	"权限",
	"记忆",
	"上下文",
	"压缩",
	"总结",
	"摘要",
	"执行",
	"提交",
	"推送",
	"代码",
	"文件",
}

func looksLikeCodeOrPath(text string) bool {
	for _, marker := range []string{".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".md", ".json", "go test", "go run", "npm ", "pnpm ", "git "} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return true
		}
	}
	return strings.Count(text, "/") >= 2
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
