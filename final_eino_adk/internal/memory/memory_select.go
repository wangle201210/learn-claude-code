package memory

import (
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/textutil"
)

func loadMemories(messages []*schema.Message) string {
	selected := selectRelevantMemories(messages)
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

func selectRelevantMemories(messages []*schema.Message) []string {
	files := listMemoryFiles()
	if len(files) == 0 {
		return nil
	}
	recent := textutil.TextFromMessages(messages, 2000)
	if strings.TrimSpace(recent) == "" {
		return nil
	}

	const maxItems = 5
	keywords := memoryKeywords(recent)
	var selected []string
	for _, f := range files {
		hay := strings.ToLower(f.name + " " + f.description + " " + f.body)
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

func memoryKeywords(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') &&
			!(r >= '0' && r <= '9') &&
			!(r >= '\u4e00' && r <= '\u9fff')
	}) {
		if len([]rune(w)) <= 1 || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}
