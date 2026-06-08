package main

import (
	"context"
	"os"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/wangle201210/learn-claude-code/internal/modelenv"
)

func NewModel(ctx context.Context) (model.ToolCallingChatModel, error) {
	if err := modelenv.Require(); err != nil {
		return nil, err
	}
	maxTokens := 8000
	cfg := &openai.ChatModelConfig{
		Model:               os.Getenv("OPENAI_MODEL"),
		APIKey:              os.Getenv("OPENAI_API_KEY"),
		BaseURL:             os.Getenv("OPENAI_BASE_URL"),
		MaxCompletionTokens: &maxTokens,
	}
	return openai.NewChatModel(ctx, cfg)
}
