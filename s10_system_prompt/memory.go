package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// memoryDir 是跨会话记忆的存储目录：每条记忆一个 .md 文件 + MEMORY.md 索引。
var (
	memoryDir   = filepath.Join(workdir, ".memory")
	memoryIndex = filepath.Join(memoryDir, "MEMORY.md")
)

const consolidateThreshold = 10 // 记忆文件数达到此值就触发整合（Dream）

type memoryFile struct {
	filename    string
	name        string
	description string
	typ         string
	body        string
}

// memoryItem 是 LLM 提取/整合时返回的 JSON 结构。
type memoryItem struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

func slugify(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "/", "-")
	return s
}

// writeMemoryFile 写入单个记忆文件（YAML frontmatter + body），然后重建索引。
func writeMemoryFile(name, typ, description, body string) {
	_ = os.MkdirAll(memoryDir, 0o755)
	path := filepath.Join(memoryDir, slugify(name)+".md")
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\ntype: %s\n---\n\n%s\n", name, description, typ, body)
	_ = os.WriteFile(path, []byte(content), 0o644)
	rebuildIndex()
}

// rebuildIndex 扫描所有记忆文件，重建 MEMORY.md 索引（每条一行）。
func rebuildIndex() {
	files := listMemoryFiles()
	var b strings.Builder
	for _, f := range files {
		fmt.Fprintf(&b, "- [%s](%s) — %s\n", f.name, f.filename, f.description)
	}
	_ = os.MkdirAll(memoryDir, 0o755)
	_ = os.WriteFile(memoryIndex, []byte(b.String()), 0o644)
}

// readMemoryIndex 读取 MEMORY.md 索引（注入 SYSTEM，每轮都在）。
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

// listMemoryFiles 列出所有记忆文件及其元数据（按文件名排序，跳过 MEMORY.md）。
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
		meta, body := parseFrontmatter(string(raw)) // 复用 skill.go 的解析
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

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// recentUserText 收集最近若干条用户消息文本，作为记忆选择/提取的上下文。
func recentUserText(messages []*schema.Message, maxChars int) string {
	var texts []string
	for i := len(messages) - 1; i >= 0 && len(texts) < 3; i-- {
		if messages[i].Role == schema.User && messages[i].Content != "" {
			texts = append([]string{messages[i].Content}, texts...)
		}
	}
	joined := strings.Join(texts, " ")
	return truncate(joined, maxChars)
}

// extractJSONArray 从 LLM 回复里抠出第一个 JSON 数组（应对包裹文本/代码块）。
func extractJSONArray(text string) string {
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start < 0 || end < 0 || end < start {
		return ""
	}
	return text[start : end+1]
}

// selectRelevantMemories 让 LLM 从记忆目录里挑出与最近对话相关的；失败回退关键词匹配。
func selectRelevantMemories(ctx context.Context, messages []*schema.Message) []string {
	files := listMemoryFiles()
	if len(files) == 0 {
		return nil
	}
	recent := recentUserText(messages, 2000)
	if strings.TrimSpace(recent) == "" {
		return nil
	}

	var catalog strings.Builder
	for i, f := range files {
		fmt.Fprintf(&catalog, "%d: %s — %s\n", i, f.name, f.description)
	}
	prompt := "Given the recent conversation and the memory catalog below, " +
		"select the indices of memories that are clearly relevant. " +
		"Return ONLY a JSON array of integers, e.g. [0, 3]. If none, return [].\n\n" +
		"Recent conversation:\n" + recent + "\n\nMemory catalog:\n" + catalog.String()

	const maxItems = 5
	if resp, err := summarizer.Generate(ctx, []*schema.Message{schema.UserMessage(prompt)}); err == nil {
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

	// 回退：关键词匹配 name + description。
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

// loadMemories 拼出相关记忆内容，供注入到上下文。
func loadMemories(ctx context.Context, messages []*schema.Message) string {
	selected := selectRelevantMemories(ctx, messages)
	if len(selected) == 0 {
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

// extractMemories 每轮结束后，从最近对话里用 LLM 提取新记忆并落盘（去重）。
func extractMemories(ctx context.Context, messages []*schema.Message) {
	var dialogue []string
	start := len(messages) - 10
	if start < 0 {
		start = 0
	}
	for _, m := range messages[start:] {
		if m.Content != "" {
			dialogue = append(dialogue, string(m.Role)+": "+m.Content)
		}
	}
	if len(dialogue) == 0 {
		return
	}

	existing := listMemoryFiles()
	existingDesc := "(none)"
	if len(existing) > 0 {
		var b strings.Builder
		for _, m := range existing {
			fmt.Fprintf(&b, "- %s: %s\n", m.name, m.description)
		}
		existingDesc = b.String()
	}

	prompt := "Extract user preferences, constraints, or project facts from this dialogue.\n" +
		"Return a JSON array. Each item: {name, type, description, body}.\n" +
		"- name: short kebab-case identifier (e.g. 'user-preference-tabs')\n" +
		"- type: one of 'user', 'feedback', 'project', 'reference'\n" +
		"- description: one-line summary for index lookup\n" +
		"- body: full detail in markdown\n" +
		"If nothing new or already covered by existing memories, return [].\n\n" +
		"Existing memories:\n" + existingDesc + "\n\nDialogue:\n" + truncate(strings.Join(dialogue, "\n"), 4000)

	resp, err := summarizer.Generate(ctx, []*schema.Message{schema.UserMessage(prompt)})
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
	for _, m := range items {
		if m.Description != "" && m.Body != "" {
			writeMemoryFile(m.Name, orDefault(m.Type, "user"), m.Description, m.Body)
			count++
		}
	}
	if count > 0 {
		fmt.Printf("\n\033[33m[Memory: extracted %d new memories]\033[0m\n", count)
	}
}

// consolidateMemories 在记忆文件数达到阈值时，用 LLM 合并去重（Dream）。
func consolidateMemories(ctx context.Context) {
	files := listMemoryFiles()
	if len(files) < consolidateThreshold {
		return
	}

	var catalog strings.Builder
	for _, f := range files {
		fmt.Fprintf(&catalog, "## %s\nname: %s\ndescription: %s\n%s\n\n", f.filename, f.name, f.description, f.body)
	}
	prompt := "Consolidate the following memory files. Rules:\n" +
		"1. Merge duplicates into one\n2. Remove outdated/contradicted memories\n" +
		"3. Keep the total under 30 memories\n4. Preserve important user preferences above all\n" +
		"Return a JSON array. Each item: {name, type, description, body}.\n\n" + truncate(catalog.String(), 16000)

	resp, err := summarizer.Generate(ctx, []*schema.Message{schema.UserMessage(prompt)})
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

	// 清掉旧记忆文件（保留 MEMORY.md），再写入整合后的。
	for _, f := range files {
		_ = os.Remove(filepath.Join(memoryDir, f.filename))
	}
	for _, m := range items {
		if m.Description != "" && m.Body != "" {
			writeMemoryFile(m.Name, orDefault(m.Type, "user"), m.Description, m.Body)
		}
	}
	fmt.Printf("\n\033[33m[Memory: consolidated %d → %d memories]\033[0m\n", len(files), len(items))
}
