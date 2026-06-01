package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// 四层压缩管线的阈值。核心原则：cheap first, expensive last。
const (
	contextLimit       = 5000   // 估算超过此值就做 L4 LLM 摘要
	keepRecent         = 3      // L2：保留最近这么多条工具结果不压缩
	persistThreshold   = 3000   // L3：单条结果超过此值才落盘
	budgetMaxBytes     = 20_000 // L3：最近一批工具结果总字节预算
	maxReactiveRetries = 1      // 反应式压缩的重试上限
	summaryInputLimit  = 80000  // 喂给摘要模型的对话上限
)

var (
	transcriptDir  = filepath.Join(workdir, ".transcripts")
	toolResultsDir = filepath.Join(workdir, ".task_outputs", "tool-results")
)

// summarizer 是无工具的底座模型，专用于生成摘要（main 里初始化）。
var summarizer model.BaseChatModel

// estimateSize 粗略估算 messages 占用的字符数（对应 Python 的 len(str(msgs))）。
func estimateSize(messages []*schema.Message) int {
	n := 0
	for _, m := range messages {
		n += len(m.Content)
		for _, tc := range m.ToolCalls {
			n += len(tc.Function.Name) + len(tc.Function.Arguments)
		}
	}
	return n
}

type messageSpan struct {
	start int
	end   int
}

// messageSpans 把消息切成“不可拆段”：
// 1. 普通消息单独成段；
// 2. assistant(tool_calls) + 紧随其后的 tool 结果视为一个原子段。
func messageSpans(messages []*schema.Message) []messageSpan {
	spans := make([]messageSpan, 0, len(messages))
	for i := 0; i < len(messages); {
		end := i + 1
		if messages[i].Role == schema.Assistant && len(messages[i].ToolCalls) > 0 {
			for end < len(messages) && messages[end].Role == schema.Tool {
				end++
			}
		}
		spans = append(spans, messageSpan{start: i, end: end})
		i = end
	}
	return spans
}

func snipWindow(messages []*schema.Message, keepHeadMessages, keepTailMessages int) (int, int, bool) {
	spans := messageSpans(messages)
	if len(spans) == 0 {
		return 0, 0, false
	}

	headSpanEnd := 0
	headCount := 0
	for headSpanEnd < len(spans) && headCount < keepHeadMessages {
		headCount += spans[headSpanEnd].end - spans[headSpanEnd].start
		headSpanEnd++
	}

	tailSpanStart := len(spans)
	tailCount := 0
	for tailSpanStart > headSpanEnd && tailCount < keepTailMessages {
		tailSpanStart--
		tailCount += spans[tailSpanStart].end - spans[tailSpanStart].start
	}

	if headSpanEnd >= tailSpanStart {
		return 0, 0, false
	}
	return spans[headSpanEnd-1].end, spans[tailSpanStart].start, true
}

func safeTail(messages []*schema.Message, keepMessages int) []*schema.Message {
	if len(messages) == 0 || keepMessages <= 0 {
		return nil
	}

	spans := messageSpans(messages)
	startSpan := len(spans)
	count := 0
	for startSpan > 0 && count < keepMessages {
		startSpan--
		count += spans[startSpan].end - spans[startSpan].start
	}

	start := spans[startSpan].start
	if len(messages) > 0 && messages[0].Role == schema.System && start == 0 {
		if len(spans) == 1 {
			return nil
		}
		start = spans[1].start
	}
	return messages[start:]
}

// ── L1 snipCompact：消息过多时裁掉中间，保留头尾 ────────────────
func snipCompact(messages []*schema.Message) []*schema.Message {
	const maxMessages = 20
	if len(messages) <= maxMessages {
		return messages
	}
	keepHead, keepTail := 3, maxMessages-3
	headEnd, tailStart, ok := snipWindow(messages, keepHead, keepTail)
	if !ok {
		return messages
	}
	snipped := tailStart - headEnd

	out := make([]*schema.Message, 0, maxMessages+1)
	out = append(out, messages[:headEnd]...)
	out = append(out, schema.UserMessage(fmt.Sprintf("[snipped %d messages]", snipped)))
	out = append(out, messages[tailStart:]...)
	return out
}

// ── L2 microCompact：把较旧的工具结果替换成占位符 ────────────────
// Eino 适配：工具结果是独立的 Role==Tool 消息，而非 user 消息里的 block。
func microCompact(messages []*schema.Message) []*schema.Message {
	var toolIdx []int
	for i, m := range messages {
		if m.Role == schema.Tool {
			toolIdx = append(toolIdx, i)
		}
	}
	if len(toolIdx) <= keepRecent {
		return messages
	}
	for _, i := range toolIdx[:len(toolIdx)-keepRecent] {
		if len(messages[i].Content) > 120 {
			messages[i].Content = "[Earlier tool result compacted. Re-run if needed.]"
		}
	}
	return messages
}

// ── L3 toolResultBudget：最近一批工具结果太大时，把大的落盘 ────────
func persistLargeOutput(id, output string) string {
	if len(output) <= persistThreshold {
		return output
	}
	if id == "" {
		id = "unknown"
	}
	_ = os.MkdirAll(toolResultsDir, 0o755)
	path := filepath.Join(toolResultsDir, id+".txt")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		_ = os.WriteFile(path, []byte(output), 0o644)
	}
	return fmt.Sprintf("<persisted-output>\nFull output: %s\nPreview:\n%s\n</persisted-output>", path, truncate(output, 2000))
}

func toolResultBudget(messages []*schema.Message) []*schema.Message {
	// 收集尾部连续的工具结果消息（对应"最后一条 user 消息里的 tool_result 们"）。
	var idx []int
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == schema.Tool {
			idx = append(idx, i)
		} else {
			break
		}
	}
	if len(idx) == 0 {
		return messages
	}

	total := 0
	for _, i := range idx {
		total += len(messages[i].Content)
	}
	if total <= budgetMaxBytes {
		return messages
	}

	// 大的优先落盘，直到回到预算之内。
	sort.Slice(idx, func(a, b int) bool {
		return len(messages[idx[a]].Content) > len(messages[idx[b]].Content)
	})
	for _, i := range idx {
		if total <= budgetMaxBytes {
			break
		}
		content := messages[i].Content
		if len(content) <= persistThreshold {
			continue
		}
		messages[i].Content = persistLargeOutput(messages[i].ToolCallID, content)
		total = total - len(content) + len(messages[i].Content)
	}
	return messages
}

// ── L4 compactHistory：估算仍超阈值时，落盘 transcript + LLM 摘要 ──
func writeTranscript(messages []*schema.Message) string {
	_ = os.MkdirAll(transcriptDir, 0o755)
	path := filepath.Join(transcriptDir, fmt.Sprintf("transcript_%d.jsonl", time.Now().Unix()))
	f, err := os.Create(path)
	if err != nil {
		return path
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, m := range messages {
		_ = enc.Encode(m)
	}
	return path
}

func summarizeHistory(ctx context.Context, messages []*schema.Message) string {
	conv, _ := json.Marshal(messages)
	prompt := "Summarize this coding-agent conversation so work can continue.\n" +
		"Preserve: 1. current goal, 2. key findings/decisions, 3. files read/changed, " +
		"4. remaining work, 5. user constraints.\nBe compact but concrete.\n\n" +
		truncate(string(conv), summaryInputLimit)

	resp, err := summarizer.Generate(ctx, []*schema.Message{schema.UserMessage(prompt)})
	if err != nil || resp.Content == "" {
		return "(empty summary)"
	}
	return resp.Content
}

// keepSystem 取出 messages 里的 system 消息（若有），压缩后仍要保留它。
func keepSystem(messages []*schema.Message) []*schema.Message {
	if len(messages) > 0 && messages[0].Role == schema.System {
		return []*schema.Message{messages[0]}
	}
	return nil
}

func compactHistory(ctx context.Context, messages []*schema.Message) []*schema.Message {
	path := writeTranscript(messages)
	fmt.Printf("[transcript saved: %s]\n", path)
	summary := summarizeHistory(ctx, messages)

	out := keepSystem(messages)
	out = append(out, schema.UserMessage("[Compacted]\n\n"+summary))
	return out
}

// reactiveCompact：API 仍报 prompt 过长时的兜底——摘要 + 最近 5 条。
func reactiveCompact(ctx context.Context, messages []*schema.Message) []*schema.Message {
	writeTranscript(messages)
	summary := summarizeHistory(ctx, messages)

	out := keepSystem(messages)
	out = append(out, schema.UserMessage("[Reactive compact]\n\n"+summary))
	out = append(out, safeTail(messages, 5)...)
	return out
}

func isPromptTooLong(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "prompt_too_long") ||
		strings.Contains(s, "too many tokens") ||
		strings.Contains(s, "context length") ||
		strings.Contains(s, "maximum context")
}

// enforcePairing 是 LLM 调用前的最后一道保险：扫描 messages，让每个
// assistant tool_call 都有对应的 Tool 输出、每条 Tool 都有前面的
// assistant 调用。无论 snipCompact / compactHistory / reactiveCompact
// 如何裁剪，出栏的 messages 里 tool_call 与 tool_output 永远成对——
// 避免 OpenAI 报 "No tool output found for function call ..."。
func enforcePairing(messages []*schema.Message) []*schema.Message {
	outputs := make(map[string]bool, len(messages))
	for _, m := range messages {
		if m.Role == schema.Tool && m.ToolCallID != "" {
			outputs[m.ToolCallID] = true
		}
	}

	keptCalls := make(map[string]bool, len(messages))
	out := make([]*schema.Message, 0, len(messages))
	for _, m := range messages {
		if m.Role == schema.Assistant && len(m.ToolCalls) > 0 {
			keep := make([]schema.ToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				if outputs[tc.ID] {
					keep = append(keep, tc)
					keptCalls[tc.ID] = true
				}
			}
			if len(keep) == 0 && strings.TrimSpace(m.Content) == "" {
				continue
			}
			copied := *m
			copied.ToolCalls = keep
			out = append(out, &copied)
			continue
		}
		out = append(out, m)
	}

	final := make([]*schema.Message, 0, len(out))
	for _, m := range out {
		if m.Role == schema.Tool && !keptCalls[m.ToolCallID] {
			continue
		}
		final = append(final, m)
	}
	return final
}
