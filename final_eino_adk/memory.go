package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const consolidateThreshold = 10

var (
	memoryDir   = filepath.Join(workdir, ".memory")
	memoryIndex = filepath.Join(memoryDir, "MEMORY.md")
)

type memoryFile struct {
	filename    string
	name        string
	description string
	typ         string
	body        string
}

type memoryItem struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

type memoryMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	model model.BaseChatModel
}

func newMemoryMiddleware(m model.BaseChatModel) adk.ChatModelAgentMiddleware {
	return &memoryMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		model:                        m,
	}
}

func (m *memoryMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	state = withoutMemoryMessages(state)
	content := loadMemories(ctx, m.model, state.Messages)
	if content == "" {
		return ctx, state, nil
	}
	nState := *state
	nState.Messages = insertMemoryMessage(nState.Messages, content)
	return ctx, &nState, nil
}

func (m *memoryMiddleware) AfterAgent(ctx context.Context, state *adk.ChatModelAgentState) (context.Context, error) {
	cleanState := withoutMemoryMessages(state)
	extractMemories(ctx, m.model, cleanState.Messages)
	consolidateMemories(ctx, m.model)
	return ctx, nil
}

func withoutMemoryMessages(state *adk.ChatModelAgentState) *adk.ChatModelAgentState {
	var filtered []*schema.Message
	changed := false
	for _, msg := range state.Messages {
		if msg.Extra != nil {
			if _, ok := msg.Extra["final_eino_memory_context"]; ok {
				changed = true
				continue
			}
		}
		filtered = append(filtered, msg)
	}
	if !changed {
		return state
	}
	nState := *state
	nState.Messages = filtered
	return &nState
}

func insertMemoryMessage(messages []*schema.Message, content string) []*schema.Message {
	msg := schema.UserMessage(content)
	msg.Extra = map[string]any{"final_eino_memory_context": true}

	out := make([]*schema.Message, 0, len(messages)+1)
	inserted := false
	for _, existing := range messages {
		if !inserted && existing.Role == schema.User {
			out = append(out, msg)
			inserted = true
		}
		out = append(out, existing)
	}
	if !inserted {
		out = append(out, msg)
	}
	return out
}

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	if s == "" {
		s = "memory"
	}
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "/", "-")
	return s
}

func writeMemoryFile(name, typ, description, body string) {
	_ = os.MkdirAll(memoryDir, 0o755)
	path := filepath.Join(memoryDir, slugify(name)+".md")
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\ntype: %s\n---\n\n%s\n", name, description, typ, body)
	_ = os.WriteFile(path, []byte(content), 0o644)
	rebuildIndex()
}

func rebuildIndex() {
	files := listMemoryFiles()
	var b strings.Builder
	for _, f := range files {
		fmt.Fprintf(&b, "- [%s](%s) - %s\n", f.name, f.filename, f.description)
	}
	_ = os.MkdirAll(memoryDir, 0o755)
	_ = os.WriteFile(memoryIndex, []byte(b.String()), 0o644)
}

func readMemoryIndex() string {
	data, err := os.ReadFile(memoryIndex)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readMemoryFile(filename string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(memoryDir, filename))
	if err != nil {
		return "", false
	}
	return string(data), true
}

func listMemoryFiles() []memoryFile {
	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && e.Name() != "MEMORY.md" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var out []memoryFile
	for _, fn := range names {
		raw, err := os.ReadFile(filepath.Join(memoryDir, fn))
		if err != nil {
			continue
		}
		meta, body := parseSimpleFrontmatter(string(raw))
		name := meta["name"]
		if name == "" {
			name = strings.TrimSuffix(fn, ".md")
		}
		out = append(out, memoryFile{
			filename:    fn,
			name:        name,
			description: meta["description"],
			typ:         orDefault(meta["type"], "user"),
			body:        body,
		})
	}
	return out
}

func parseSimpleFrontmatter(text string) (map[string]string, string) {
	meta := map[string]string{}
	if !strings.HasPrefix(text, "---") {
		return meta, text
	}
	parts := strings.SplitN(text, "---", 3)
	if len(parts) < 3 {
		return meta, text
	}
	for _, line := range strings.Split(strings.TrimSpace(parts[1]), "\n") {
		if i := strings.Index(line, ":"); i >= 0 {
			k := strings.TrimSpace(line[:i])
			v := strings.Trim(strings.TrimSpace(line[i+1:]), `"'`)
			meta[k] = v
		}
	}
	return meta, strings.TrimSpace(parts[2])
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

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

func extractMemories(ctx context.Context, m model.BaseChatModel, messages []*schema.Message) {
	dialogue := textFromMessages(tailMessages(messages, 10), 4000)
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
	arr := extractJSONArray(resp.Content)
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
		"Each item: {name, type, description, body}.\n\n" + truncate(catalog.String(), 16000)

	resp, err := m.Generate(ctx, []*schema.Message{schema.UserMessage(prompt)})
	if err != nil {
		return
	}
	arr := extractJSONArray(resp.Content)
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

func tailMessages(messages []*schema.Message, n int) []*schema.Message {
	if len(messages) <= n {
		return messages
	}
	return messages[len(messages)-n:]
}
