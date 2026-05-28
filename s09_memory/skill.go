package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skillsDir：技能目录，结构为 skills/<name>/SKILL.md。
var skillsDir = filepath.Join(workdir, "skills")

type skill struct {
	name        string
	description string
	content     string // SKILL.md 完整原文（按需注入时返回）
}

var (
	skillRegistry = map[string]skill{}
	skillOrder    []string // 保持扫描顺序，让 catalog 输出稳定
)

// parseFrontmatter 解析 SKILL.md 的 YAML frontmatter，返回 (meta, body)。
// 教学用极简解析：只认顶层 "key: value" 行（多行块标量等高级语法不处理）。
func parseFrontmatter(text string) (map[string]string, string) {
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

// scanSkills 扫描 skills/ 目录，把每个 SKILL.md 的 name/描述/完整内容存进 registry。
func scanSkills() {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		raw, err := os.ReadFile(filepath.Join(skillsDir, dir, "SKILL.md"))
		if err != nil {
			continue
		}
		text := string(raw)
		meta, _ := parseFrontmatter(text)

		name := meta["name"]
		if name == "" {
			name = dir
		}
		desc := meta["description"]
		if desc == "" {
			first, _, _ := strings.Cut(text, "\n")
			desc = strings.TrimSpace(strings.TrimLeft(first, "#"))
		}
		skillRegistry[name] = skill{name: name, description: desc, content: text}
		skillOrder = append(skillOrder, name)
	}
}

func init() {
	scanSkills()
}

// listSkills 输出 catalog：每个技能的名字 + 一行描述（廉价，常驻 SYSTEM）。
func listSkills() string {
	if len(skillOrder) == 0 {
		return "(no skills found)"
	}
	var b strings.Builder
	for _, n := range skillOrder {
		s := skillRegistry[n]
		fmt.Fprintf(&b, "- **%s**: %s\n", s.name, s.description)
	}
	return strings.TrimRight(b.String(), "\n")
}

// buildSystem 构建 SYSTEM：技能目录（s07）+ 记忆索引（s09），都只放廉价的摘要。
func buildSystem() string {
	s := fmt.Sprintf("You are a coding agent at %s. Skills available:\n%s\n"+
		"Use load_skill to get full details when needed.", workdir, listSkills())
	if index := readMemoryIndex(); index != "" {
		s += "\n\nMemories available:\n" + index +
			"\nRespect user preferences from memory. When the user says 'remember' or " +
			"expresses a clear preference, it will be extracted as a memory."
	}
	return s
}

// loadSkill 是 load_skill 工具的 handler —— 第二层（昂贵）：按需返回完整内容。
// 只用 registry 查表（不接受任意路径），避免路径遍历。
func loadSkill(ctx context.Context, args string) string {
	var a struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	s, ok := skillRegistry[a.Name]
	if !ok {
		return "Skill not found: " + a.Name
	}
	return s.content
}
