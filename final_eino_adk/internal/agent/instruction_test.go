package agent

import (
	"context"
	"strings"
	"testing"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/tool"
)

func TestBuildInstructionReflectsDirectDynamicToolsAndSkills(t *testing.T) {
	instruction, err := buildInstruction(context.Background(), instructionInput{
		Workspace: "/repo",
		DirectTools: []tool.BaseTool{
			namedTestTool{name: "task", desc: "Task tool."},
			namedTestTool{name: "background_execute", desc: "Background tool."},
		},
		DynamicTools: []tool.BaseTool{
			namedTestTool{name: "mcp__docs__search", desc: "Search docs."},
		},
		SkillBackend: instructionSkillBackend{
			items: []einoskill.FrontMatter{
				{Name: "review", Description: "Review code."},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"You are a coding agent at /repo.",
		"Direct agent tools: background_execute, task.",
		"Use task for isolated subtasks.",
		"MCP tools are configured as deferred tools (1 available). Use tool_search",
		"review: Review code.",
	} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("instruction missing %q:\n%s", want, instruction)
		}
	}
	if strings.Contains(instruction, "appear as normal tools") {
		t.Fatalf("instruction should not describe deferred MCP tools as normal tools:\n%s", instruction)
	}
}

func TestBuildInstructionOmitsMissingCapabilities(t *testing.T) {
	instruction, err := buildInstruction(context.Background(), instructionInput{
		Workspace:   "/repo",
		DirectTools: []tool.BaseTool{namedTestTool{name: "task", desc: "Task tool."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, notWant := range []string{"tool_search", "Skills available", "background_execute"} {
		if strings.Contains(instruction, notWant) {
			t.Fatalf("instruction unexpectedly contains %q:\n%s", notWant, instruction)
		}
	}
}

type instructionSkillBackend struct {
	items []einoskill.FrontMatter
}

func (b instructionSkillBackend) List(context.Context) ([]einoskill.FrontMatter, error) {
	return b.items, nil
}

func (b instructionSkillBackend) Get(context.Context, string) (einoskill.Skill, error) {
	return einoskill.Skill{}, nil
}
