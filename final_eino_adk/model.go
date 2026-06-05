package main

import (
	"context"
	"os"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// NewModel 返回主模型：从 OPENAI_API_KEY / OPENAI_MODEL / OPENAI_BASE_URL 读配置。
// 与 s01_agent_loop 的同名函数等价；这里作为根目录最终版 agent 的主底座。
func NewModel(ctx context.Context) (model.ToolCallingChatModel, error) {
	maxTokens := 80000
	base, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		Model:               os.Getenv("OPENAI_MODEL"),
		APIKey:              os.Getenv("OPENAI_API_KEY"),
		BaseURL:             os.Getenv("OPENAI_BASE_URL"),
		MaxCompletionTokens: &maxTokens,
	})
	if err != nil {
		return nil, err
	}
	return newRoutedModel(base, modelRouterConfigFromEnv()), nil
}

// NewFallbackModel 在 OPENAI_FALLBACK_MODEL 环境变量存在时，返回一个备用模型实例
// （复用同一 BASE_URL/API_KEY，仅切换模型名）；否则返回 (nil, nil)。供
// adk.ModelFailoverConfig.GetFailoverModel 在主模型过载时切换使用。
func NewFallbackModel(ctx context.Context) (model.ToolCallingChatModel, error) {
	name := os.Getenv("OPENAI_FALLBACK_MODEL")
	if name == "" {
		return nil, nil
	}
	maxTokens := 80000
	return openai.NewChatModel(ctx, &openai.ChatModelConfig{
		Model:               name,
		APIKey:              os.Getenv("OPENAI_API_KEY"),
		BaseURL:             os.Getenv("OPENAI_BASE_URL"),
		MaxCompletionTokens: &maxTokens,
	})
}
