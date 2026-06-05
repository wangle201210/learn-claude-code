package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/tool"
)

type instructionInput struct {
	Workspace    string
	DirectTools  []tool.BaseTool
	DynamicTools []tool.BaseTool
	SkillBackend einoskill.Backend
}

type instructionContext struct {
	Workspace        string         `json:"workspace"`
	DirectToolNames  []string       `json:"direct_tool_names"`
	DynamicToolCount int            `json:"dynamic_tool_count"`
	SkillCatalog     []skillSummary `json:"skill_catalog"`
}

type skillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

var instructionCache = struct {
	sync.Mutex
	key   string
	value string
}{}

func buildInstruction(ctx context.Context, input instructionInput) (string, error) {
	c, err := buildInstructionContext(ctx, input)
	if err != nil {
		return "", err
	}
	keyBytes, _ := json.Marshal(c)
	key := string(keyBytes)

	instructionCache.Lock()
	defer instructionCache.Unlock()
	if instructionCache.key == key && instructionCache.value != "" {
		return instructionCache.value, nil
	}
	instructionCache.key = key
	instructionCache.value = assembleInstruction(c)
	return instructionCache.value, nil
}

func buildInstructionContext(ctx context.Context, input instructionInput) (instructionContext, error) {
	directNames, err := collectToolNames(ctx, input.DirectTools)
	if err != nil {
		return instructionContext{}, err
	}
	dynamicNames, err := collectToolNames(ctx, input.DynamicTools)
	if err != nil {
		return instructionContext{}, err
	}
	skills, err := listSkillSummaries(ctx, input.SkillBackend)
	if err != nil {
		return instructionContext{}, err
	}
	return instructionContext{
		Workspace:        input.Workspace,
		DirectToolNames:  directNames,
		DynamicToolCount: len(dynamicNames),
		SkillCatalog:     skills,
	}, nil
}

func collectToolNames(ctx context.Context, tools []tool.BaseTool) ([]string, error) {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		info, err := t.Info(ctx)
		if err != nil {
			return nil, err
		}
		if info != nil && info.Name != "" {
			names = append(names, info.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func listSkillSummaries(ctx context.Context, backend einoskill.Backend) ([]skillSummary, error) {
	if backend == nil {
		return nil, nil
	}
	frontMatters, err := backend.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]skillSummary, 0, len(frontMatters))
	for _, item := range frontMatters {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		out = append(out, skillSummary{
			Name:        name,
			Description: strings.TrimSpace(item.Description),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func assembleInstruction(c instructionContext) string {
	var sections []string
	sections = append(sections,
		fmt.Sprintf("You are a coding agent at %s. Use tools to solve tasks. Act, don't explain.", c.Workspace),
		"Respect workspace boundaries. Ask before risky writes or commands unless policy already allows them.",
	)

	if len(c.DirectToolNames) > 0 {
		sections = append(sections, "Direct agent tools: "+strings.Join(c.DirectToolNames, ", ")+".")
	}

	guidance := toolGuidance(c.DirectToolNames)
	if len(guidance) > 0 {
		sections = append(sections, strings.Join(guidance, "\n"))
	}

	if c.DynamicToolCount > 0 {
		sections = append(sections,
			fmt.Sprintf("MCP tools are configured as deferred tools (%d available). Use tool_search to discover or select them before calling any mcp__* tool directly.", c.DynamicToolCount),
		)
	}

	if len(c.SkillCatalog) > 0 {
		sections = append(sections, "Skills available through the Eino skill tool:\n"+formatSkillCatalog(c.SkillCatalog))
	}

	return strings.Join(sections, "\n\n")
}

func toolGuidance(names []string) []string {
	available := map[string]bool{}
	for _, name := range names {
		available[name] = true
	}
	var lines []string
	addIf := func(name, line string) {
		if available[name] {
			lines = append(lines, line)
		}
	}
	addIf("task", "- Use task for isolated subtasks.")
	addIf("teammate", "- Use teammate for bounded peer review or research.")
	addIf("spawn_teammate", "- Use spawn_teammate when a teammate should continue independently and report back later.")
	addIf("compact", "- Use compact when earlier conversation history should be summarized for more context budget.")
	addIf("background_execute", "- Use background_execute for long-running commands and check_notifications/background_status for results.")
	addIf("schedule_cron", "- Use schedule_cron/list_crons/cancel_cron for autonomous scheduled prompts.")
	addIf("send_message", "- Use send_message/check_inbox/request_plan/review_plan/request_shutdown for protocol coordination.")
	addIf("create_worktree", "- Use create_worktree/remove_worktree/keep_worktree when work should be isolated in a git worktree.")
	return lines
}

func formatSkillCatalog(skills []skillSummary) string {
	lines := make([]string, 0, len(skills))
	for _, skill := range skills {
		if skill.Description == "" {
			lines = append(lines, "- "+skill.Name)
			continue
		}
		lines = append(lines, "- "+skill.Name+": "+skill.Description)
	}
	return strings.Join(lines, "\n")
}
