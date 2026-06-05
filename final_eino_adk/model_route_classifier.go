package main

import (
	"strings"
	"unicode"

	"github.com/cloudwego/eino/schema"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/recovery"
)

func classifyModelRoute(messages []*schema.Message) modelRouteDecision {
	stats := collectRouteStats(messages)
	if isCompactConfirmationPrompt(stats.latestUser) {
		return modelRouteDecision{tier: modelRouteSimple, reason: "compact confirmation"}
	}
	if isMemoryHelperPrompt(stats.latestUser) || isSummarizationHelperPrompt(stats.latestUser) {
		return modelRouteDecision{tier: modelRouteStandard, reason: "helper prompt"}
	}
	if stats.latestUser == "" && stats.hasSummaryContext {
		return modelRouteDecision{tier: modelRouteStandard, reason: "summary context"}
	}
	if stats.totalChars > 8000 || stats.messageCount > 16 {
		return modelRouteDecision{tier: modelRouteComplex, reason: "large context"}
	}
	if isComplexPrompt(stats.latestUser) || stats.hasErrorTraffic {
		return modelRouteDecision{tier: modelRouteComplex, reason: "complex prompt"}
	}
	if stats.hasToolTraffic {
		return modelRouteDecision{tier: modelRouteStandard, reason: "tool traffic"}
	}
	if isStandardPrompt(stats.latestUser) {
		return modelRouteDecision{tier: modelRouteStandard, reason: "coding prompt"}
	}
	return modelRouteDecision{tier: modelRouteSimple, reason: "short prompt"}
}

type routeStats struct {
	latestUser        string
	totalChars        int
	messageCount      int
	hasToolTraffic    bool
	hasErrorTraffic   bool
	hasSummaryContext bool
}

func collectRouteStats(messages []*schema.Message) routeStats {
	var stats routeStats
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if msg.Role == schema.System || isTransientContextMessage(msg) {
			continue
		}
		if isEinoSummaryMessage(msg) {
			stats.hasSummaryContext = true
			continue
		}
		stats.messageCount++
		stats.totalChars += len(msg.Content)
		if msg.Role == schema.Tool || len(msg.ToolCalls) > 0 {
			stats.hasToolTraffic = true
		}
		if containsAny(strings.ToLower(msg.Content), errorMarkers) {
			stats.hasErrorTraffic = true
		}
		if msg.Role == schema.User {
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
	if _, ok := msg.Extra["final_eino_hook_context"]; ok {
		return true
	}
	if _, ok := msg.Extra["__toolsearch_reminder__"]; ok {
		return true
	}
	if recovery.IsControlMessage(msg) {
		return true
	}
	return false
}

func isEinoSummaryMessage(msg *schema.Message) bool {
	if msg == nil || msg.Extra == nil {
		return false
	}
	return msg.Extra["_eino_summarization_content_type"] == "summary"
}

func isComplexPrompt(prompt string) bool {
	text := strings.ToLower(strings.TrimSpace(prompt))
	if text == "" {
		return false
	}
	if len([]rune(text)) > 500 {
		return true
	}
	return containsAny(text, errorMarkers) ||
		containsAny(text, complexPromptKeywords) ||
		isComplexPlanningPrompt(text)
}

func isStandardPrompt(prompt string) bool {
	text := strings.ToLower(strings.TrimSpace(prompt))
	if text == "" {
		return false
	}
	return containsAny(text, standardPromptKeywords) || looksLikeCodeOrPath(text)
}

func isMemoryHelperPrompt(prompt string) bool {
	text := strings.TrimSpace(prompt)
	return strings.HasPrefix(text, "Extract durable user preferences, constraints, or project facts") ||
		strings.HasPrefix(text, "Consolidate the following memory files.")
}

func isSummarizationHelperPrompt(prompt string) bool {
	text := strings.TrimSpace(prompt)
	return strings.Contains(text, "Your task is to create a detailed summary of the conversation so far") ||
		strings.Contains(text, "你的任务是对目前为止的对话创建一份详细的总结")
}

func isCompactConfirmationPrompt(prompt string) bool {
	return strings.HasPrefix(strings.TrimSpace(prompt), "Context compaction is complete.")
}

var complexPromptKeywords = []string{
	"debug",
	"failing",
	"failure",
	"permission",
	"memory",
	"compact",
	"summarize",
	"summary",
	"context",
	"mcp",
	"worktree",
	"architecture",
	"security",
	"regression",
	"root cause",
	"race condition",
	"feature parity",
	"complete coverage",
	"full implementation",
	"deadlock",
	"leak",
	"调试",
	"报错",
	"异常",
	"重构",
	"权限",
	"记忆",
	"上下文",
	"压缩",
	"总结",
	"摘要",
	"架构",
	"安全",
	"回归",
	"根因",
	"竞态",
	"完整实现",
	"功能完整性",
	"全量对齐",
	"死锁",
	"泄漏",
}

func isComplexPlanningPrompt(text string) bool {
	if !containsAny(text, planningPromptKeywords) {
		return false
	}
	return containsAny(text, engineeringPlanContextKeywords) || looksLikeCodeOrPath(text)
}

var planningPromptKeywords = []string{
	"plan",
	"proposal",
	"strategy",
	"design",
	"review",
	"audit",
	"evaluate",
	"compare",
	"tradeoff",
	"trade-off",
	"migration",
	"migrate",
	"port",
	"adapt",
	"reference",
	"计划",
	"规划",
	"方案",
	"设计",
	"评审",
	"审查",
	"审核",
	"检查",
	"核实",
	"验证",
	"确认",
	"评估",
	"对比",
	"取舍",
	"迁移",
	"移植",
	"参考",
}

var engineeringPlanContextKeywords = []string{
	"agent",
	"api",
	"auth",
	"authentication",
	"authorization",
	"code",
	"codebase",
	"database",
	"deployment",
	"distributed",
	"eino",
	"implementation",
	"middleware",
	"model",
	"module",
	"performance",
	"permission",
	"plugin",
	"privacy",
	"project",
	"repo",
	"repository",
	"scalability",
	"service",
	"storage",
	"system",
	"test",
	"tool",
	"workflow",
	"代码",
	"仓库",
	"部署",
	"服务",
	"工具",
	"工作流",
	"接口",
	"鉴权",
	"模型",
	"模块",
	"权限",
	"认证",
	"实现",
	"数据库",
	"系统",
	"性能",
	"项目",
	"隐私",
	"中间件",
	"智能体",
	"插件",
}

var standardPromptKeywords = []string{
	"implement",
	"refactor",
	"fix",
	"test",
	"project",
	"repo",
	"repository",
	"codebase",
	"feature",
	"tool",
	"run",
	"execute",
	"push",
	"commit",
	"修改",
	"实现",
	"补充",
	"完善",
	"修复",
	"测试",
	"执行",
	"提交",
	"推送",
	"代码",
	"文件",
	"项目",
	"仓库",
	"功能",
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

func containsAny(text string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

var errorMarkers = []string{"```", "panic:", "traceback", "exception", "node runerror", "error:", "failed"}
