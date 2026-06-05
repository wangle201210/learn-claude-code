package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type memoryFile struct {
	filename    string
	name        string
	description string
	typ         string
	body        string
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
