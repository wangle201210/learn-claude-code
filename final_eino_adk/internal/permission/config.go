package permission

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Allow       []Rule
	Ask         []Rule
	Deny        []Rule
	DefaultMode string
}

type Rule struct {
	Raw     string
	Tool    string
	Content string
}

type settingsFile struct {
	Permissions permissionSettings `json:"permissions"`
}

type permissionSettings struct {
	Allow       []string `json:"allow"`
	Ask         []string `json:"ask"`
	Deny        []string `json:"deny"`
	DefaultMode string   `json:"defaultMode"`
}

func LoadConfig(root string) (Config, error) {
	var cfg Config
	for _, path := range []string{
		filepath.Join(root, ".claude", "settings.json"),
		filepath.Join(root, ".claude", "settings.local.json"),
	} {
		next, err := readPermissionConfigFile(path)
		if err != nil {
			return Config{}, err
		}
		cfg.Allow = append(cfg.Allow, next.Allow...)
		cfg.Ask = append(cfg.Ask, next.Ask...)
		cfg.Deny = append(cfg.Deny, next.Deny...)
		if next.DefaultMode != "" {
			cfg.DefaultMode = next.DefaultMode
		}
	}
	return cfg, nil
}

func readPermissionConfigFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	var settings settingsFile
	if err := json.Unmarshal(data, &settings); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return permissionSettingsToConfig(settings.Permissions)
}

func permissionSettingsToConfig(settings permissionSettings) (Config, error) {
	allow, err := parseRules(settings.Allow)
	if err != nil {
		return Config{}, err
	}
	ask, err := parseRules(settings.Ask)
	if err != nil {
		return Config{}, err
	}
	deny, err := parseRules(settings.Deny)
	if err != nil {
		return Config{}, err
	}
	return Config{
		Allow:       allow,
		Ask:         ask,
		Deny:        deny,
		DefaultMode: strings.TrimSpace(settings.DefaultMode),
	}, nil
}

func parseRules(rawRules []string) ([]Rule, error) {
	rules := make([]Rule, 0, len(rawRules))
	for _, raw := range rawRules {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		rule, err := parseRule(raw)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func parseRule(raw string) (Rule, error) {
	open := firstUnescaped(raw, '(')
	if open < 0 {
		return Rule{Raw: raw, Tool: normalizeRuleTool(raw)}, nil
	}
	close := lastUnescaped(raw, ')')
	if close <= open || close != len(raw)-1 {
		return Rule{Raw: raw, Tool: normalizeRuleTool(raw)}, nil
	}
	tool := strings.TrimSpace(raw[:open])
	if tool == "" {
		return Rule{Raw: raw, Tool: normalizeRuleTool(raw)}, nil
	}
	content := unescapeRuleContent(raw[open+1 : close])
	if content == "" || content == "*" {
		return Rule{Raw: raw, Tool: normalizeRuleTool(tool)}, nil
	}
	return Rule{Raw: raw, Tool: normalizeRuleTool(tool), Content: content}, nil
}

func firstUnescaped(s string, target byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == target && !escapedAt(s, i) {
			return i
		}
	}
	return -1
}

func lastUnescaped(s string, target byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == target && !escapedAt(s, i) {
			return i
		}
	}
	return -1
}

func escapedAt(s string, idx int) bool {
	count := 0
	for i := idx - 1; i >= 0 && s[i] == '\\'; i-- {
		count++
	}
	return count%2 == 1
}

func unescapeRuleContent(content string) string {
	var out strings.Builder
	out.Grow(len(content))
	escaped := false
	for _, r := range content {
		if escaped {
			out.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		out.WriteRune(r)
	}
	if escaped {
		out.WriteRune('\\')
	}
	return out.String()
}
