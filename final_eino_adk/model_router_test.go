package main

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/modelroute"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/recovery"
)

func TestModelRouterClassifiesSimplePrompt(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{schema.UserMessage("你好，解释一下 defer")})
	if decision.tier != modelRouteSimple {
		t.Fatalf("tier = %s, want simple (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterIgnoresLongSystemPromptForSimpleUserPrompt(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{
		{Role: schema.System, Content: strings.Repeat("system instructions ", 900)},
		schema.UserMessage("hello"),
	})
	if decision.tier != modelRouteSimple {
		t.Fatalf("tier = %s, want simple (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterIgnoresEinoSummaryContentForSimpleUserPrompt(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{
		einoSummaryMessage("Summary says prior context had panic: failed memory compact errors."),
		schema.UserMessage("hello"),
	})
	if decision.tier != modelRouteSimple {
		t.Fatalf("tier = %s, want simple (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterIgnoresHookContextForSimpleUserPrompt(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{
		transientHookMessage("Hook context says panic: failed permission architecture review."),
		schema.UserMessage("hello"),
	})
	if decision.tier != modelRouteSimple {
		t.Fatalf("tier = %s, want simple (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterIgnoresToolSearchReminderForSimpleUserPrompt(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{
		transientToolSearchReminder("<available-deferred-tools>\nmcp__docs__search\nmcp__deploy__rollout\n</available-deferred-tools>"),
		schema.UserMessage("hello"),
	})
	if decision.tier != modelRouteSimple {
		t.Fatalf("tier = %s, want simple (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterIgnoresRecoveryContinuationForLatestUserPrompt(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{
		schema.UserMessage("hello"),
		recovery.ContinuationMessage(),
	})
	if decision.tier != modelRouteSimple {
		t.Fatalf("tier = %s, want simple (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterClassifiesSummaryOnlyContextAsStandard(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{
		einoSummaryMessage("Summary says prior context had panic: failed memory compact errors."),
	})
	if decision.tier != modelRouteStandard {
		t.Fatalf("tier = %s, want standard (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterUsesContextTierHint(t *testing.T) {
	decision := routeDecision(
		modelroute.WithTier(context.Background(), modelroute.Standard),
		[]*schema.Message{schema.UserMessage("hello")},
	)
	if decision.tier != modelRouteStandard {
		t.Fatalf("tier = %s, want standard (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterClassifiesComplexPrompt(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{schema.UserMessage("修复 final_eino_adk 里面上下文压缩和 memory 的异常，并补测试")})
	if decision.tier != modelRouteComplex {
		t.Fatalf("tier = %s, want complex (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterClassifiesEngineeringPlanAsComplex(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{schema.UserMessage(
		"plan the migration strategy for the plugin architecture and permission model",
	)})
	if decision.tier != modelRouteComplex {
		t.Fatalf("tier = %s, want complex (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterClassifiesSecurityReviewAsComplex(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{schema.UserMessage(
		"review the authentication and permission architecture for security risks",
	)})
	if decision.tier != modelRouteComplex {
		t.Fatalf("tier = %s, want complex (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterKeepsEverydayPlanningSimple(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{schema.UserMessage("帮我列一个明天晚饭计划")})
	if decision.tier != modelRouteSimple {
		t.Fatalf("tier = %s, want simple (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterKeepsEverydayVerificationSimple(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{schema.UserMessage("帮我核实一下明天上海天气")})
	if decision.tier != modelRouteSimple {
		t.Fatalf("tier = %s, want simple (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterClassifiesProjectCompletenessAuditAsComplex(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{schema.UserMessage("核实 final_eino_adk 是否完整实现 s1-s19 里面提到的功能")})
	if decision.tier != modelRouteComplex {
		t.Fatalf("tier = %s, want complex (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterClassifiesToolTrafficAsStandard(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{
		schema.UserMessage("what happened"),
		{Role: schema.Tool, Content: "tool output"},
	})
	if decision.tier != modelRouteStandard {
		t.Fatalf("tier = %s, want standard (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterClassifiesErroredToolTrafficAsComplex(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{
		schema.UserMessage("what happened"),
		{Role: schema.Tool, Content: "panic: failed to read file"},
	})
	if decision.tier != modelRouteComplex {
		t.Fatalf("tier = %s, want complex (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterClassifiesMemoryHelperAsStandard(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{schema.UserMessage(
		"Extract durable user preferences, constraints, or project facts from this dialogue.\nReturn a JSON array.",
	)})
	if decision.tier != modelRouteStandard {
		t.Fatalf("tier = %s, want standard (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterClassifiesSummarizationHelperAsStandard(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{schema.UserMessage(
		"CRITICAL: Respond with TEXT ONLY.\n\nYour task is to create a detailed summary of the conversation so far.",
	)})
	if decision.tier != modelRouteStandard {
		t.Fatalf("tier = %s, want standard (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterClassifiesChineseSummarizationHelperAsStandard(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{schema.UserMessage(
		"关键：仅以文本响应。\n\n你的任务是对目前为止的对话创建一份详细的总结，需要密切关注用户的明确请求。",
	)})
	if decision.tier != modelRouteStandard {
		t.Fatalf("tier = %s, want standard (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterClassifiesCompactConfirmationAsSimple(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{schema.UserMessage(
		"Context compaction is complete. Reply in one short Chinese sentence confirming the conversation history was compacted.",
	)})
	if decision.tier != modelRouteSimple {
		t.Fatalf("tier = %s, want simple (%s)", decision.tier, decision.reason)
	}
}

func TestModelRouterKeepsUserSummaryDebugPromptComplex(t *testing.T) {
	decision := classifyModelRoute([]*schema.Message{schema.UserMessage("总结这个上下文压缩异常的根因，并修复代码")})
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

func TestModelRouterRecordsPerRunUsage(t *testing.T) {
	base := &captureModel{usage: &schema.TokenUsage{
		PromptTokens:     3,
		CompletionTokens: 2,
		TotalTokens:      5,
	}}
	routed := newRoutedModel(base, modelRouterConfig{
		defaultModel:  "complex-model",
		simpleModel:   "simple-model",
		standardModel: "standard-model",
		complexModel:  "complex-model",
	})
	ctx, usage := withModelRouteUsage(context.Background())

	if _, err := routed.Generate(ctx, []*schema.Message{schema.UserMessage("hello")}); err != nil {
		t.Fatal(err)
	}
	if _, err := routed.Generate(ctx, []*schema.Message{schema.UserMessage("implement a small fix in foo.go")}); err != nil {
		t.Fatal(err)
	}
	if _, err := routed.Generate(ctx, []*schema.Message{schema.UserMessage("debug a race condition in memory compact")}); err != nil {
		t.Fatal(err)
	}

	want := "model route 本轮: simple=1(simple-model){tokens=5,in=3,out=2}, standard=1(standard-model){tokens=5,in=3,out=2}, complex=1(complex-model){tokens=5,in=3,out=2}"
	if got := formatModelRouteUsage(usage); got != want {
		t.Fatalf("usage = %q, want %q", got, want)
	}
}

func TestModelRouterSumsUsageAcrossCalls(t *testing.T) {
	base := &captureModel{usage: &schema.TokenUsage{
		PromptTokens:     3,
		CompletionTokens: 2,
		TotalTokens:      5,
	}}
	routed := newRoutedModel(base, modelRouterConfig{
		defaultModel:  "complex-model",
		simpleModel:   "simple-model",
		standardModel: "standard-model",
		complexModel:  "complex-model",
	})
	ctx, usage := withModelRouteUsage(context.Background())

	if _, err := routed.Generate(ctx, []*schema.Message{schema.UserMessage("hello")}); err != nil {
		t.Fatal(err)
	}
	if _, err := routed.Generate(ctx, []*schema.Message{schema.UserMessage("hi")}); err != nil {
		t.Fatal(err)
	}

	want := "model route 本轮: simple=2(simple-model){tokens=10,in=6,out=4}"
	if got := formatModelRouteUsage(usage); got != want {
		t.Fatalf("usage = %q, want %q", got, want)
	}
}

func TestModelRouterRecordsStreamUsage(t *testing.T) {
	base := &captureModel{usage: &schema.TokenUsage{
		PromptTokens:     10,
		CompletionTokens: 4,
		TotalTokens:      14,
		PromptTokenDetails: schema.PromptTokenDetails{
			CachedTokens: 6,
		},
	}}
	routed := newRoutedModel(base, modelRouterConfig{
		defaultModel:  "complex-model",
		simpleModel:   "simple-model",
		standardModel: "standard-model",
		complexModel:  "complex-model",
	})
	ctx, usage := withModelRouteUsage(context.Background())

	reader, err := routed.Stream(ctx, []*schema.Message{schema.UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Recv(); err != nil {
		t.Fatal(err)
	}

	want := "model route 本轮: simple=1(simple-model){tokens=14,in=10,out=4,cache=6}"
	if got := formatModelRouteUsage(usage); got != want {
		t.Fatalf("usage = %q, want %q", got, want)
	}
}

func TestModelRouterKeepsMaxUsageForRepeatedStreamChunks(t *testing.T) {
	base := &captureModel{
		streamMessages: []*schema.Message{
			responseWithUsage(3, 1, 4, 0),
			responseWithUsage(10, 4, 14, 6),
		},
	}
	routed := newRoutedModel(base, modelRouterConfig{
		defaultModel:  "complex-model",
		simpleModel:   "simple-model",
		standardModel: "standard-model",
		complexModel:  "complex-model",
	})
	ctx, usage := withModelRouteUsage(context.Background())

	reader, err := routed.Stream(ctx, []*schema.Message{schema.UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Recv(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Recv(); err != nil {
		t.Fatal(err)
	}

	want := "model route 本轮: simple=1(simple-model){tokens=14,in=10,out=4,cache=6}"
	if got := formatModelRouteUsage(usage); got != want {
		t.Fatalf("usage = %q, want %q", got, want)
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

func TestModelRouterUsesStandardModelForCodingPrompt(t *testing.T) {
	base := &captureModel{}
	routed := newRoutedModel(base, modelRouterConfig{
		defaultModel:  "complex-model",
		simpleModel:   "simple-model",
		standardModel: "standard-model",
		complexModel:  "complex-model",
	})

	_, err := routed.Generate(context.Background(), []*schema.Message{schema.UserMessage("implement a small fix in foo.go")})
	if err != nil {
		t.Fatal(err)
	}

	if got := base.lastModel(); got != "standard-model" {
		t.Fatalf("routed model = %q, want standard-model", got)
	}
}

func TestModelRouterUsesHintedBaseModel(t *testing.T) {
	base := &captureModel{}
	routed := newRoutedModel(base, modelRouterConfig{
		defaultModel:  "complex-model",
		simpleModel:   "simple-model",
		standardModel: "standard-model",
		complexModel:  "complex-model",
	})
	hinted := modelroute.WrapBase(routed, modelroute.Standard)

	_, err := hinted.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}

	if got := base.lastModel(); got != "standard-model" {
		t.Fatalf("routed model = %q, want standard-model", got)
	}
}

func TestModelRouterUsesComplexModelForHardPrompt(t *testing.T) {
	base := &captureModel{}
	routed := newRoutedModel(base, modelRouterConfig{
		defaultModel:  "standard-model",
		simpleModel:   "simple-model",
		standardModel: "standard-model",
		complexModel:  "complex-model",
	})

	_, err := routed.Generate(context.Background(), []*schema.Message{schema.UserMessage("debug a race condition in the memory compact middleware")})
	if err != nil {
		t.Fatal(err)
	}

	if got := base.lastModel(); got != "complex-model" {
		t.Fatalf("routed model = %q, want complex-model", got)
	}
}

func TestModelRouterUsesStandardModelForMemoryHelper(t *testing.T) {
	base := &captureModel{}
	routed := newRoutedModel(base, modelRouterConfig{
		defaultModel:  "complex-model",
		simpleModel:   "simple-model",
		standardModel: "standard-model",
		complexModel:  "complex-model",
	})

	_, err := routed.Generate(context.Background(), []*schema.Message{schema.UserMessage("Consolidate the following memory files. Merge duplicates.")})
	if err != nil {
		t.Fatal(err)
	}

	if got := base.lastModel(); got != "standard-model" {
		t.Fatalf("routed model = %q, want standard-model", got)
	}
}

func TestModelRouterUsesStandardModelForSummarizationHelper(t *testing.T) {
	base := &captureModel{}
	routed := newRoutedModel(base, modelRouterConfig{
		defaultModel:  "complex-model",
		simpleModel:   "simple-model",
		standardModel: "standard-model",
		complexModel:  "complex-model",
	})

	_, err := routed.Generate(context.Background(), []*schema.Message{schema.UserMessage("Your task is to create a detailed summary of the conversation so far.")})
	if err != nil {
		t.Fatal(err)
	}

	if got := base.lastModel(); got != "standard-model" {
		t.Fatalf("routed model = %q, want standard-model", got)
	}
}

func TestModelRouterUsesSimpleModelForCompactConfirmation(t *testing.T) {
	base := &captureModel{}
	routed := newRoutedModel(base, modelRouterConfig{
		defaultModel:  "complex-model",
		simpleModel:   "simple-model",
		standardModel: "standard-model",
		complexModel:  "complex-model",
	})

	_, err := routed.Generate(context.Background(), []*schema.Message{schema.UserMessage("Context compaction is complete. Reply in one short Chinese sentence confirming the conversation history was compacted.")})
	if err != nil {
		t.Fatal(err)
	}

	if got := base.lastModel(); got != "simple-model" {
		t.Fatalf("routed model = %q, want simple-model", got)
	}
}

func TestModelRouterKeepsHintedToolCallingModelAfterWithTools(t *testing.T) {
	base := &captureModel{}
	routed := newRoutedModel(base, modelRouterConfig{
		defaultModel:  "complex-model",
		simpleModel:   "simple-model",
		standardModel: "standard-model",
		complexModel:  "complex-model",
	})
	hinted, err := modelroute.WrapToolCalling(routed, modelroute.Standard).WithTools([]*schema.ToolInfo{{Name: "read_file"}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = hinted.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}

	if got := base.lastModel(); got != "standard-model" {
		t.Fatalf("routed model = %q, want standard-model", got)
	}
}

func TestModelRouterFallsBackToDefaultForStandardTier(t *testing.T) {
	base := &captureModel{}
	routed := newRoutedModel(base, modelRouterConfig{
		defaultModel: "complex-model",
		simpleModel:  "simple-model",
		complexModel: "complex-model",
	})

	_, err := routed.Generate(context.Background(), []*schema.Message{schema.UserMessage("implement a small fix in foo.go")})
	if err != nil {
		t.Fatal(err)
	}

	if got := base.lastModel(); got != "" {
		t.Fatalf("routed model override = %q, want none for default standard fallback", got)
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

func TestModelRouterRecordsExplicitModelUsage(t *testing.T) {
	base := &captureModel{usage: &schema.TokenUsage{
		PromptTokens:     4,
		CompletionTokens: 3,
		TotalTokens:      7,
	}}
	routed := newRoutedModel(base, modelRouterConfig{
		defaultModel:  "complex-model",
		simpleModel:   "simple-model",
		standardModel: "standard-model",
		complexModel:  "complex-model",
	})
	ctx, usage := withModelRouteUsage(context.Background())

	_, err := routed.Generate(ctx, []*schema.Message{schema.UserMessage("hello")}, model.WithModel("manual-model"))
	if err != nil {
		t.Fatal(err)
	}

	want := "model route 本轮: explicit=1(manual-model){tokens=7,in=4,out=3}"
	if got := formatModelRouteUsage(usage); got != want {
		t.Fatalf("usage = %q, want %q", got, want)
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

func TestModelRouterFormatsEmptyUsageAsEmpty(t *testing.T) {
	if got := formatModelRouteUsage(nil); got != "" {
		t.Fatalf("usage = %q, want empty", got)
	}
	_, usage := withModelRouteUsage(context.Background())
	if got := formatModelRouteUsage(usage); got != "" {
		t.Fatalf("usage = %q, want empty", got)
	}
}

type captureModel struct {
	mu             sync.Mutex
	models         []string
	usage          *schema.TokenUsage
	streamMessages []*schema.Message
}

func (m *captureModel) Generate(_ context.Context, _ []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.capture(opts...)
	return m.response(), nil
}

func (m *captureModel) Stream(_ context.Context, _ []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.capture(opts...)
	if len(m.streamMessages) > 0 {
		return schema.StreamReaderFromArray(m.streamMessages), nil
	}
	return schema.StreamReaderFromArray([]*schema.Message{m.response()}), nil
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

func (m *captureModel) response() *schema.Message {
	msg := schema.AssistantMessage("ok", nil)
	if m.usage != nil {
		usage := *m.usage
		msg.ResponseMeta = &schema.ResponseMeta{Usage: &usage}
	}
	return msg
}

func responseWithUsage(prompt, completion, total, cached int) *schema.Message {
	msg := schema.AssistantMessage("ok", nil)
	msg.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		PromptTokenDetails: schema.PromptTokenDetails{
			CachedTokens: cached,
		},
	}}
	return msg
}

func einoSummaryMessage(content string) *schema.Message {
	msg := schema.UserMessage(content)
	msg.Extra = map[string]any{"_eino_summarization_content_type": "summary"}
	return msg
}

func transientHookMessage(content string) *schema.Message {
	msg := schema.UserMessage(content)
	msg.Extra = map[string]any{"final_eino_hook_context": true}
	return msg
}

func transientToolSearchReminder(content string) *schema.Message {
	msg := schema.UserMessage(content)
	msg.Extra = map[string]any{"__toolsearch_reminder__": true}
	return msg
}
