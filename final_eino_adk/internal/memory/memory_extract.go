package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/textutil"
)

const consolidateThreshold = 10

var memoryTriggerTerms = []string{
	"remember",
	"memory",
	"preference",
	"prefer",
	"constraint",
	"记住",
	"记忆",
	"偏好",
	"习惯",
	"约束",
	"要求",
	"以后",
	"下次",
}

type memoryItem struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

func extractMemories(ctx context.Context, m model.BaseChatModel, messages []*schema.Message) {
	dialogue := textutil.TextFromMessages(tailMessages(messages, 10), 4000)
	if strings.TrimSpace(dialogue) == "" {
		return
	}

	existing := listMemoryFiles()
	existingDesc := "(none)"
	if len(existing) > 0 {
		var b strings.Builder
		for _, item := range existing {
			fmt.Fprintf(&b, "- %s: %s\n", item.name, item.description)
		}
		existingDesc = b.String()
	}

	prompt := "Extract durable user preferences, constraints, or project facts from this dialogue.\n" +
		"Return a JSON array. Each item: {name, type, description, body}.\n" +
		"- name: short kebab-case identifier\n" +
		"- type: one of 'user', 'feedback', 'project', 'reference'\n" +
		"- description: one-line summary for index lookup\n" +
		"- body: full detail in markdown\n" +
		"If nothing new or already covered by existing memories, return [].\n\n" +
		"Existing memories:\n" + existingDesc + "\n\nDialogue:\n" + dialogue

	resp, err := m.Generate(ctx, []*schema.Message{schema.UserMessage(prompt)})
	if err != nil {
		return
	}
	arr := textutil.ExtractJSONArray(resp.Content)
	if arr == "" {
		return
	}
	var items []memoryItem
	if json.Unmarshal([]byte(arr), &items) != nil {
		return
	}
	count := 0
	for _, item := range items {
		if item.Description != "" && item.Body != "" {
			writeMemoryFile(item.Name, orDefault(item.Type, "user"), item.Description, item.Body)
			count++
		}
	}
	if count > 0 {
		fmt.Printf("\n\033[33m[Memory: extracted %d new memories]\033[0m\n", count)
	}
}

func consolidateMemories(ctx context.Context, m model.BaseChatModel) {
	files := listMemoryFiles()
	if len(files) < consolidateThreshold {
		return
	}

	var catalog strings.Builder
	for _, f := range files {
		fmt.Fprintf(&catalog, "## %s\nname: %s\ndescription: %s\n%s\n\n", f.filename, f.name, f.description, f.body)
	}
	prompt := "Consolidate the following memory files. Merge duplicates, remove outdated memories, " +
		"and preserve important user preferences. Return a JSON array. " +
		"Each item: {name, type, description, body}.\n\n" + textutil.Truncate(catalog.String(), 16000)

	resp, err := m.Generate(ctx, []*schema.Message{schema.UserMessage(prompt)})
	if err != nil {
		return
	}
	arr := textutil.ExtractJSONArray(resp.Content)
	if arr == "" {
		return
	}
	var items []memoryItem
	if json.Unmarshal([]byte(arr), &items) != nil || len(items) == 0 {
		return
	}

	for _, f := range files {
		_ = os.Remove(filepath.Join(memoryDir, f.filename))
	}
	for _, item := range items {
		if item.Description != "" && item.Body != "" {
			writeMemoryFile(item.Name, orDefault(item.Type, "user"), item.Description, item.Body)
		}
	}
	fmt.Printf("\n\033[33m[Memory: consolidated %d -> %d memories]\033[0m\n", len(files), len(items))
}

func shouldExtractMemories(messages []*schema.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg == nil || msg.Role != schema.User {
			continue
		}
		content := strings.ToLower(msg.Content)
		for _, term := range memoryTriggerTerms {
			if strings.Contains(content, strings.ToLower(term)) {
				return true
			}
		}
		return false
	}
	return false
}

func tailMessages(messages []*schema.Message, n int) []*schema.Message {
	if len(messages) <= n {
		return messages
	}
	return messages[len(messages)-n:]
}
