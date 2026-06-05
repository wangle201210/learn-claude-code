package main

import (
	"context"

	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/modelroute"
)

type modelRouteUsageCollector = modelroute.UsageCollector

func withModelRouteUsage(ctx context.Context) (context.Context, *modelRouteUsageCollector) {
	return modelroute.WithUsage(ctx)
}

func formatModelRouteUsage(collector *modelRouteUsageCollector) string {
	return modelroute.FormatUsage(collector)
}
