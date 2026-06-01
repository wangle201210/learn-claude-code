package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// promptContext 是 SYSTEM prompt 的输入：全部来自真实状态（启用了哪些工具、
// 有没有技能/记忆），而非硬编码。context 不变时，组装结果就能复用（缓存）。
type promptContext struct {
	Workspace    string   `json:"workspace"`
	EnabledTools []string `json:"enabled_tools"`
	SkillCatalog string   `json:"skill_catalog"`
	MemoryIndex  string   `json:"memory_index"`
}

// buildContext 从真实状态派生 context：工作区、启用的工具、技能目录、记忆索引。
// 注意：相关记忆（LLM 动态挑选）不放这里——它每轮可能变，会破坏缓存；
// 由 agentLoop 单独拼接。
func buildContext() promptContext {
	return promptContext{
		Workspace:    workdir,
		EnabledTools: toolNames(),
		SkillCatalog: listSkills(),
		MemoryIndex:  readMemoryIndex(),
	}
}

func toolNames() []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.info.Name)
	}
	return names
}

// assembleSystemPrompt 按 context 选择并拼接分段：identity/tools/guidance always，
// skills/memory 仅在真实状态满足时加入（累积了 s05-s09 各章的提示片段）。
func assembleSystemPrompt(c promptContext) string {
	var sections []string

	// always
	sections = append(sections, fmt.Sprintf("You are a coding agent at %s. Act, don't explain.", c.Workspace))
	sections = append(sections, "Available tools: "+strings.Join(c.EnabledTools, ", ")+".")
	sections = append(sections, "Before starting any multi-step task, use todo_write to plan your steps. "+
		"For complex sub-problems, use the task tool to spawn a subagent.")

	// conditional
	if c.SkillCatalog != "" && c.SkillCatalog != "(no skills found)" {
		sections = append(sections, "Skills available:\n"+c.SkillCatalog+"\nUse load_skill to get full details when needed.")
	}
	if c.MemoryIndex != "" {
		sections = append(sections, "Memories available:\n"+c.MemoryIndex+
			"\nRespect user preferences from memory. When the user says 'remember', it will be extracted as a memory.")
	}

	return strings.Join(sections, "\n\n")
}

// 进程内缓存：context 没变就不重新拼字符串。用 json 做确定性 key（不用 Go 的
// map 迭代序/指针），仅避免进程内重复组装；真正的 API 级 prompt 缓存还要靠
// 稳定的分段顺序（这里 sections 顺序固定）。
var (
	lastCtxKey string
	lastPrompt string
)

func getSystemPrompt(c promptContext) string {
	key, _ := json.Marshal(c)
	if string(key) == lastCtxKey && lastPrompt != "" {
		fmt.Println("  \033[90m[cache hit] system prompt unchanged\033[0m")
		return lastPrompt
	}
	lastCtxKey = string(key)
	lastPrompt = assembleSystemPrompt(c)

	loaded := []string{"identity", "tools", "guidance"}
	if c.SkillCatalog != "" && c.SkillCatalog != "(no skills found)" {
		loaded = append(loaded, "skills")
	}
	if c.MemoryIndex != "" {
		loaded = append(loaded, "memory")
	}
	fmt.Printf("  \033[32m[assembled] sections: %s\033[0m\n", strings.Join(loaded, ", "))
	return lastPrompt
}
