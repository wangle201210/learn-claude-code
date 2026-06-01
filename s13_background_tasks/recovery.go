package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const (
	escalatedMaxTokens = 64000
	defaultMaxTokens   = 8000
	maxRecoveryRetries = 3 // continuation prompt 的最大次数
	maxRetries         = 10
	baseDelayMs        = 500
	maxConsecutive529  = 3
	continuationPrompt = "Output token limit hit. Resume directly — no apology, no recap. Pick up mid-thought."
)

// fallbackModel 在连续 529 过载时切换的备用模型（可选，来自环境变量）。
var fallbackModel = os.Getenv("FALLBACK_MODEL")

// recoveryState 跨循环跟踪各类恢复尝试。
type recoveryState struct {
	hasEscalated                bool   // 是否已把 max_tokens 升到 64K
	recoveryCount               int    // continuation prompt 用了几次
	consecutive529              int    // 连续 529 计数
	hasAttemptedReactiveCompact bool   // 是否已做过反应式压缩
	currentModel                string // 非空表示已切到 fallback 模型
}

// retryDelay 指数退避 + 抖动（上限 32s）。
func retryDelay(attempt int) time.Duration {
	base := math.Min(float64(baseDelayMs)*math.Pow(2, float64(attempt)), 32000) / 1000.0
	jitter := rand.Float64() * base * 0.25
	return time.Duration((base + jitter) * float64(time.Second))
}

func is429(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "429") || strings.Contains(s, "rate limit") || strings.Contains(s, "ratelimit")
}

func is529(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "529") || strings.Contains(s, "overloaded")
}

// generateWithRetry 包装一次 Generate：对瞬时错误（429/529）做指数退避重试，
// 连续 529 达上限则切换到 fallback 模型；非瞬时错误直接返回给外层处理。
// 每次重试都按最新 state（含 fallback 模型）重建 options。
func generateWithRetry(ctx context.Context, agent model.ToolCallingChatModel, messages []*schema.Message, maxTokens int, state *recoveryState) (*schema.Message, error) {
	for attempt := 0; attempt < maxRetries; attempt++ {
		opts := []model.Option{model.WithMaxTokens(maxTokens)}
		if state.currentModel != "" {
			opts = append(opts, model.WithModel(state.currentModel))
		}

		resp, err := agent.Generate(ctx, messages, opts...)
		if err == nil {
			state.consecutive529 = 0
			return resp, nil
		}

		switch {
		case is429(err):
			delay := retryDelay(attempt)
			fmt.Printf("  \033[33m[429 rate limit] retry %d/%d, wait %.1fs\033[0m\n", attempt+1, maxRetries, delay.Seconds())
			time.Sleep(delay)

		case is529(err):
			state.consecutive529++
			if state.consecutive529 >= maxConsecutive529 {
				if fallbackModel != "" {
					state.currentModel = fallbackModel
					state.consecutive529 = 0
					fmt.Printf("  \033[31m[529 x%d] switching to %s\033[0m\n", maxConsecutive529, fallbackModel)
				} else {
					state.consecutive529 = 0
					fmt.Printf("  \033[31m[529 x%d] no FALLBACK_MODEL configured, continuing retry\033[0m\n", maxConsecutive529)
				}
			}
			delay := retryDelay(attempt)
			fmt.Printf("  \033[33m[529 overloaded] retry %d/%d, wait %.1fs\033[0m\n", attempt+1, maxRetries, delay.Seconds())
			time.Sleep(delay)

		default:
			// 非瞬时错误（如 prompt_too_long、其它）交给外层 recovery 处理。
			return nil, err
		}
	}
	return nil, fmt.Errorf("max retries (%d) exceeded", maxRetries)
}
