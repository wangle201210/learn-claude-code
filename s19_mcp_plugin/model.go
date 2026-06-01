package main

import (
	"context"
	"os"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

func NewModel(ctx context.Context) (model.ToolCallingChatModel, error) {
	maxTokens := 8000
	cfg := &openai.ChatModelConfig{
		Model:               os.Getenv("OPENAI_MODEL"),
		APIKey:              os.Getenv("OPENAI_API_KEY"),
		BaseURL:             os.Getenv("OPENAI_BASE_URL"),
		MaxCompletionTokens: &maxTokens,
	}
	return openai.NewChatModel(ctx, cfg)
}
