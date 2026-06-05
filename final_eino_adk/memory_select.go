package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func loadMemories(ctx context.Context, m model.BaseChatModel, messages []*schema.Message) string {
	selected := selectRelevantMemories(ctx, m, messages)
	if len(selected) == 0 {
		if index := readMemoryIndex(); index != "" {
			return "<memory_index>\n" + index + "\n</memory_index>"
		}
		return ""
	}
	parts := []string{"<relevant_memories>"}
	for _, fn := range selected {
		if content, ok := readMemoryFile(fn); ok {
			parts = append(parts, content)
		}
	}
	parts = append(parts, "</relevant_memories>")
	return strings.Join(parts, "\n\n")
}

func selectRelevantMemories(ctx context.Context, m model.BaseChatModel, messages []*schema.Message) []string {
	files := listMemoryFiles()
	if len(files) == 0 {
		return nil
	}
	recent := textFromMessages(messages, 2000)
	if strings.TrimSpace(recent) == "" {
		return nil
	}

	var catalog strings.Builder
	for i, f := range files {
		fmt.Fprintf(&catalog, "%d: %s - %s\n", i, f.name, f.description)
	}
	prompt := "Given the recent conversation and the memory catalog below, " +
		"select the indices of memories that are clearly relevant. " +
		"Return ONLY a JSON array of integers, e.g. [0, 3]. If none, return [].\n\n" +
		"Recent conversation:\n" + recent + "\n\nMemory catalog:\n" + catalog.String()

	const maxItems = 5
	if resp, err := m.Generate(ctx, []*schema.Message{schema.UserMessage(prompt)}); err == nil {
		if arr := extractJSONArray(resp.Content); arr != "" {
			var indices []int
			if json.Unmarshal([]byte(arr), &indices) == nil {
				var selected []string
				for _, idx := range indices {
					if idx >= 0 && idx < len(files) {
						selected = append(selected, files[idx].filename)
						if len(selected) >= maxItems {
							break
						}
					}
				}
				return selected
			}
		}
	}

	var keywords []string
	for _, w := range strings.Fields(strings.ToLower(recent)) {
		if len(w) > 3 {
			keywords = append(keywords, w)
		}
	}
	var selected []string
	for _, f := range files {
		hay := strings.ToLower(f.name + " " + f.description)
		for _, kw := range keywords {
			if strings.Contains(hay, kw) {
				selected = append(selected, f.filename)
				break
			}
		}
		if len(selected) >= maxItems {
			break
		}
	}
	return selected
}
