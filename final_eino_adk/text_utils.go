package main

import (
	"strings"

	"github.com/cloudwego/eino/schema"
)

func firstString(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, _ := args[key].(string); v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "\n... (truncated)"
}

func extractJSONArray(text string) string {
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start < 0 || end < 0 || end < start {
		return ""
	}
	return text[start : end+1]
}

func textFromMessages(messages []*schema.Message, maxChars int) string {
	var parts []string
	for _, msg := range messages {
		if msg.Content == "" {
			continue
		}
		parts = append(parts, string(msg.Role)+": "+msg.Content)
	}
	return truncate(strings.Join(parts, "\n"), maxChars)
}
