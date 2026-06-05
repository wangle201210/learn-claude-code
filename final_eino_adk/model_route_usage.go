package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/cloudwego/eino/schema"
)

type modelRouteUsageKey struct {
	tier  modelRouteTier
	model string
}

type modelRouteUsageCollector struct {
	mu      sync.Mutex
	nextID  int
	records map[int]modelRouteUsageRecord
}

type modelRouteUsageRecord struct {
	key   modelRouteUsageKey
	usage modelRouteUsage
}

type modelRouteUsage struct {
	Calls            int
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CachedTokens     int
}

type modelRouteUsageContextKey struct{}

func withModelRouteUsage(ctx context.Context) (context.Context, *modelRouteUsageCollector) {
	collector := &modelRouteUsageCollector{records: map[int]modelRouteUsageRecord{}}
	return context.WithValue(ctx, modelRouteUsageContextKey{}, collector), collector
}

func recordModelRoute(ctx context.Context, tier modelRouteTier, selected string) (*modelRouteUsageCollector, int) {
	collector, ok := ctx.Value(modelRouteUsageContextKey{}).(*modelRouteUsageCollector)
	if !ok || collector == nil {
		return nil, 0
	}
	if selected == "" {
		selected = "default"
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.nextID++
	id := collector.nextID
	collector.records[id] = modelRouteUsageRecord{
		key: modelRouteUsageKey{tier: tier, model: selected},
		usage: modelRouteUsage{
			Calls: 1,
		},
	}
	return collector, id
}

func formatModelRouteUsage(collector *modelRouteUsageCollector) string {
	if collector == nil {
		return ""
	}
	counts := collector.snapshot()
	if len(counts) == 0 {
		return ""
	}

	var parts []string
	for _, tier := range []modelRouteTier{modelRouteSimple, modelRouteStandard, modelRouteComplex} {
		modelUsage := map[string]modelRouteUsage{}
		total := 0
		for key, usage := range counts {
			if key.tier != tier {
				continue
			}
			modelUsage[key.model] = addRouteUsage(modelUsage[key.model], usage)
			total += usage.Calls
		}
		if total == 0 {
			continue
		}
		models := make([]string, 0, len(modelUsage))
		for name := range modelUsage {
			models = append(models, name)
		}
		sort.Strings(models)
		if len(models) == 1 {
			parts = append(parts, formatTierUsage(tier, total, models[0], modelUsage[models[0]]))
			continue
		}
		var modelParts []string
		for _, name := range models {
			usage := modelUsage[name]
			modelParts = append(modelParts, fmt.Sprintf("%s:%d%s", name, usage.Calls, formatTokenSuffix(usage)))
		}
		parts = append(parts, fmt.Sprintf("%s=%d[%s]", tier, total, strings.Join(modelParts, ",")))
	}
	if len(parts) == 0 {
		return ""
	}
	return "model route 本轮: " + strings.Join(parts, ", ")
}

func formatTierUsage(tier modelRouteTier, calls int, modelName string, usage modelRouteUsage) string {
	return fmt.Sprintf("%s=%d(%s)%s", tier, calls, modelName, formatTokenSuffix(usage))
}

func formatTokenSuffix(usage modelRouteUsage) string {
	if usage.TotalTokens <= 0 {
		return ""
	}
	parts := []string{fmt.Sprintf("tokens=%d", usage.TotalTokens)}
	if usage.PromptTokens > 0 {
		parts = append(parts, fmt.Sprintf("in=%d", usage.PromptTokens))
	}
	if usage.CompletionTokens > 0 {
		parts = append(parts, fmt.Sprintf("out=%d", usage.CompletionTokens))
	}
	if usage.CachedTokens > 0 {
		parts = append(parts, fmt.Sprintf("cache=%d", usage.CachedTokens))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func addRouteUsage(a, b modelRouteUsage) modelRouteUsage {
	return modelRouteUsage{
		Calls:            a.Calls + b.Calls,
		PromptTokens:     a.PromptTokens + b.PromptTokens,
		CompletionTokens: a.CompletionTokens + b.CompletionTokens,
		TotalTokens:      a.TotalTokens + b.TotalTokens,
		CachedTokens:     a.CachedTokens + b.CachedTokens,
	}
}

func (c *modelRouteUsageCollector) addUsage(callID int, usage *schema.TokenUsage) {
	if usage == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	record, ok := c.records[callID]
	if !ok {
		return
	}
	record.usage.PromptTokens = max(record.usage.PromptTokens, usage.PromptTokens)
	record.usage.CompletionTokens = max(record.usage.CompletionTokens, usage.CompletionTokens)
	record.usage.TotalTokens = max(record.usage.TotalTokens, usage.TotalTokens)
	record.usage.CachedTokens = max(record.usage.CachedTokens, usage.PromptTokenDetails.CachedTokens)
	c.records[callID] = record
}

func (c *modelRouteUsageCollector) snapshot() map[modelRouteUsageKey]modelRouteUsage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[modelRouteUsageKey]modelRouteUsage{}
	for _, record := range c.records {
		out[record.key] = addRouteUsage(out[record.key], record.usage)
	}
	return out
}
