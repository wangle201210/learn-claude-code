package modelroute

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/cloudwego/eino/schema"
)

type usageKey struct {
	tier  Tier
	model string
}

type UsageCollector struct {
	mu      sync.Mutex
	nextID  int
	records map[int]usageRecord
}

type usageRecord struct {
	key   usageKey
	usage Usage
}

type Usage struct {
	Calls            int
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CachedTokens     int
}

type usageContextKey struct{}

func WithUsage(ctx context.Context) (context.Context, *UsageCollector) {
	collector := &UsageCollector{records: map[int]usageRecord{}}
	return context.WithValue(ctx, usageContextKey{}, collector), collector
}

func Record(ctx context.Context, tier Tier, selected string) (*UsageCollector, int) {
	if !validTier(tier) {
		return nil, 0
	}
	collector, ok := ctx.Value(usageContextKey{}).(*UsageCollector)
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
	collector.records[id] = usageRecord{
		key: usageKey{tier: tier, model: selected},
		usage: Usage{
			Calls: 1,
		},
	}
	return collector, id
}

func FormatUsage(collector *UsageCollector) string {
	if collector == nil {
		return ""
	}
	counts := collector.snapshot()
	if len(counts) == 0 {
		return ""
	}

	var parts []string
	for _, tier := range []Tier{Simple, Standard, Complex} {
		modelUsage := map[string]Usage{}
		total := 0
		for key, usage := range counts {
			if key.tier != tier {
				continue
			}
			modelUsage[key.model] = addUsage(modelUsage[key.model], usage)
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

func formatTierUsage(tier Tier, calls int, modelName string, usage Usage) string {
	return fmt.Sprintf("%s=%d(%s)%s", tier, calls, modelName, formatTokenSuffix(usage))
}

func formatTokenSuffix(usage Usage) string {
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

func addUsage(a, b Usage) Usage {
	return Usage{
		Calls:            a.Calls + b.Calls,
		PromptTokens:     a.PromptTokens + b.PromptTokens,
		CompletionTokens: a.CompletionTokens + b.CompletionTokens,
		TotalTokens:      a.TotalTokens + b.TotalTokens,
		CachedTokens:     a.CachedTokens + b.CachedTokens,
	}
}

func (c *UsageCollector) AddUsage(callID int, usage *schema.TokenUsage) {
	if c == nil || usage == nil {
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

func (c *UsageCollector) snapshot() map[usageKey]Usage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[usageKey]Usage{}
	for _, record := range c.records {
		out[record.key] = addUsage(out[record.key], record.usage)
	}
	return out
}
