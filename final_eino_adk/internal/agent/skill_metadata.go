package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"gopkg.in/yaml.v3"
)

type claudeSkillMetadata struct {
	Name                   string
	DisableModelInvocation bool
	Paths                  []string
}

type claudeSkillBackend struct {
	base     einoskill.Backend
	metadata map[string]claudeSkillMetadata
}

func (b *claudeSkillBackend) List(ctx context.Context) ([]einoskill.FrontMatter, error) {
	items, err := b.base.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]einoskill.FrontMatter, 0, len(items))
	for _, item := range items {
		if b.modelCanInvoke(item.Name) {
			out = append(out, item)
		}
	}
	return out, nil
}

func (b *claudeSkillBackend) Get(ctx context.Context, name string) (einoskill.Skill, error) {
	if !b.modelCanInvoke(name) {
		return einoskill.Skill{}, fmt.Errorf("skill not available for model invocation: %s", name)
	}
	return b.base.Get(ctx, name)
}

func (b *claudeSkillBackend) modelCanInvoke(name string) bool {
	meta, ok := b.metadata[strings.TrimSpace(name)]
	if !ok {
		return true
	}
	return !meta.DisableModelInvocation && !hasConditionalPaths(meta.Paths)
}

func hasConditionalPaths(paths []string) bool {
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path != "" && path != "**" && path != "*" {
			return true
		}
	}
	return false
}

func loadClaudeSkillMetadata(baseDir string) (map[string]claudeSkillMetadata, error) {
	files, err := filepath.Glob(filepath.Join(baseDir, "*", "SKILL.md"))
	if err != nil {
		return nil, err
	}
	out := map[string]claudeSkillMetadata{}
	for _, file := range files {
		meta, err := readClaudeSkillMetadata(file)
		if err != nil {
			return nil, err
		}
		name := meta.Name
		if name == "" {
			name = filepath.Base(filepath.Dir(file))
		}
		out[name] = meta
	}
	return out, nil
}

func readClaudeSkillMetadata(path string) (claudeSkillMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return claudeSkillMetadata{}, err
	}
	frontmatter, ok := rawFrontmatter(string(data))
	if !ok {
		return claudeSkillMetadata{}, nil
	}
	var raw struct {
		Name                   string `yaml:"name"`
		DisableModelInvocation any    `yaml:"disable-model-invocation"`
		Paths                  any    `yaml:"paths"`
	}
	if err := yaml.Unmarshal([]byte(frontmatter), &raw); err != nil {
		return claudeSkillMetadata{}, fmt.Errorf("parse skill metadata %s: %w", path, err)
	}
	return claudeSkillMetadata{
		Name:                   strings.TrimSpace(raw.Name),
		DisableModelInvocation: parseFrontmatterBool(raw.DisableModelInvocation),
		Paths:                  parseFrontmatterStringList(raw.Paths),
	}, nil
}

func rawFrontmatter(data string) (string, bool) {
	data = strings.TrimSpace(data)
	if !strings.HasPrefix(data, "---") {
		return "", false
	}
	rest := data[len("---"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", false
	}
	return strings.TrimSpace(rest[:end]), true
}

func parseFrontmatterBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		default:
			return false
		}
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	default:
		return false
	}
}

func parseFrontmatterStringList(value any) []string {
	var out []string
	switch v := value.(type) {
	case string:
		for _, item := range strings.Split(v, ",") {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
	case []string:
		for _, item := range v {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
