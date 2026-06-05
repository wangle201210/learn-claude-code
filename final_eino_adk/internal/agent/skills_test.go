package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/workspace"
)

func TestCompositeSkillBackendMergesAndOverridesByLaterBackend(t *testing.T) {
	ctx := context.Background()
	backend := &compositeSkillBackend{backends: []einoskill.Backend{
		&testSkillBackend{skills: []einoskill.Skill{
			{
				FrontMatter: einoskill.FrontMatter{Name: "project", Description: "project skill"},
				Content:     "project content",
			},
			{
				FrontMatter: einoskill.FrontMatter{Name: "shared", Description: "old shared"},
				Content:     "old content",
			},
		}},
		&testSkillBackend{skills: []einoskill.Skill{
			{
				FrontMatter: einoskill.FrontMatter{Name: "shared", Description: "local shared"},
				Content:     "local content",
			},
			{
				FrontMatter: einoskill.FrontMatter{Name: "local", Description: "local skill"},
				Content:     "local only",
			},
		}},
	}}

	list, err := backend.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := skillNames(list); got != "project,shared,local" {
		t.Fatalf("skills = %q, want project,shared,local", got)
	}
	if list[1].Description != "local shared" {
		t.Fatalf("shared description = %q, want local override", list[1].Description)
	}

	shared, err := backend.Get(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if shared.Content != "local content" {
		t.Fatalf("shared content = %q, want local override", shared.Content)
	}
}

func TestSkillDirsIncludesClaudeSkills(t *testing.T) {
	dirs := skillDirs("/repo")
	if len(dirs) != 2 {
		t.Fatalf("skill dirs = %#v, want root skills and .claude skills", dirs)
	}
	if dirs[0] != "/repo/skills" {
		t.Fatalf("first dir = %q, want /repo/skills", dirs[0])
	}
	if dirs[1] != "/repo/.claude/skills" {
		t.Fatalf("second dir = %q, want /repo/.claude/skills", dirs[1])
	}
}

func TestClaudeSkillBackendFiltersModelDisabledSkills(t *testing.T) {
	ctx := context.Background()
	base := &testSkillBackend{skills: []einoskill.Skill{
		{FrontMatter: einoskill.FrontMatter{Name: "safe", Description: "visible"}},
		{FrontMatter: einoskill.FrontMatter{Name: "dangerous", Description: "hidden"}},
	}}
	backend := &claudeSkillBackend{
		base: base,
		metadata: map[string]claudeSkillMetadata{
			"dangerous": {DisableModelInvocation: true},
		},
	}

	list, err := backend.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := skillNames(list); got != "safe" {
		t.Fatalf("skills = %q, want safe", got)
	}
	if _, err := backend.Get(ctx, "dangerous"); err == nil {
		t.Fatal("disabled skill should not be available to model invocation")
	}
	if _, err := backend.Get(ctx, "safe"); err != nil {
		t.Fatalf("safe skill should remain available: %v", err)
	}
}

func TestClaudeSkillBackendFiltersConditionalPathSkills(t *testing.T) {
	ctx := context.Background()
	base := &testSkillBackend{skills: []einoskill.Skill{
		{FrontMatter: einoskill.FrontMatter{Name: "go-only", Description: "conditional"}},
		{FrontMatter: einoskill.FrontMatter{Name: "always", Description: "visible"}},
	}}
	backend := &claudeSkillBackend{
		base: base,
		metadata: map[string]claudeSkillMetadata{
			"go-only": {Paths: []string{"**/*.go"}},
			"always":  {Paths: []string{"**"}},
		},
	}

	list, err := backend.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := skillNames(list); got != "always" {
		t.Fatalf("skills = %q, want always", got)
	}
}

func TestClaudeSkillBackendAllowsUnrestrictedOverride(t *testing.T) {
	ctx := context.Background()
	base := &testSkillBackend{skills: []einoskill.Skill{
		{FrontMatter: einoskill.FrontMatter{Name: "shared", Description: "override"}},
	}}
	backend := &claudeSkillBackend{
		base: base,
		metadata: map[string]claudeSkillMetadata{
			"shared": {},
		},
	}

	list, err := backend.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := skillNames(list); got != "shared" {
		t.Fatalf("skills = %q, want shared", got)
	}
	if _, err := backend.Get(ctx, "shared"); err != nil {
		t.Fatalf("unrestricted override should be available: %v", err)
	}
}

func TestLoadClaudeSkillMetadataUsesFrontmatterName(t *testing.T) {
	root := t.TempDir()
	writeSkillFile(t, filepath.Join(root, "dir-name", "SKILL.md"), `---
name: actual-name
description: hidden
disable-model-invocation: true
paths: "**/*.go, **/*.md"
---
content
`)

	metadata, err := loadClaudeSkillMetadata(root)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := metadata["actual-name"]
	if !ok {
		t.Fatalf("metadata keys = %#v, want actual-name", metadata)
	}
	if !meta.DisableModelInvocation {
		t.Fatal("disable-model-invocation was not parsed")
	}
	if len(meta.Paths) != 2 || meta.Paths[0] != "**/*.go" || meta.Paths[1] != "**/*.md" {
		t.Fatalf("paths = %#v, want sorted parsed paths", meta.Paths)
	}
}

func TestSkillModelHubAppliesFrontmatterModel(t *testing.T) {
	t.Setenv("FINAL_EINO_SIMPLE_MODEL", "cheap-model")
	base := &skillHubTestModel{}
	hub := newSkillModelHub(base)
	resolved, err := hub.Get(context.Background(), "haiku")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := resolved.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")}); err != nil {
		t.Fatal(err)
	}
	if got := base.lastModel(); got != "cheap-model" {
		t.Fatalf("model override = %q, want cheap-model", got)
	}
}

func TestResolveSkillModelNameSupportsTierAliases(t *testing.T) {
	t.Setenv("FINAL_EINO_SIMPLE_MODEL", "simple-model")
	t.Setenv("FINAL_EINO_STANDARD_MODEL", "standard-model")
	t.Setenv("FINAL_EINO_COMPLEX_MODEL", "complex-model")

	cases := map[string]string{
		"haiku":    "simple-model",
		"sonnet":   "standard-model",
		"opus":     "complex-model",
		"inherit":  "",
		"custom-x": "custom-x",
	}
	for input, want := range cases {
		if got := resolveSkillModelName(input); got != want {
			t.Fatalf("resolveSkillModelName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSkillModelHubInheritsPrimaryForInherit(t *testing.T) {
	base := &skillHubTestModel{}
	hub := newSkillModelHub(base)
	resolved, err := hub.Get(context.Background(), "inherit")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != model.BaseChatModel(base) {
		t.Fatal("inherit should return primary model")
	}
}

func TestSkillAgentHubBuildsForkAgent(t *testing.T) {
	ctx := context.Background()
	backend, err := workspace.New(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	hub := newSkillAgentHub(&skillHubTestModel{}, backend, nil)
	agent, err := hub.Get(ctx, "skill_test_agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if agent == nil {
		t.Fatal("agent is nil")
	}
}

type testSkillBackend struct {
	skills []einoskill.Skill
}

func (b *testSkillBackend) List(context.Context) ([]einoskill.FrontMatter, error) {
	out := make([]einoskill.FrontMatter, 0, len(b.skills))
	for _, skill := range b.skills {
		out = append(out, skill.FrontMatter)
	}
	return out, nil
}

func (b *testSkillBackend) Get(_ context.Context, name string) (einoskill.Skill, error) {
	for _, skill := range b.skills {
		if skill.Name == name {
			return skill, nil
		}
	}
	return einoskill.Skill{}, errTestSkillNotFound
}

var errTestSkillNotFound = errString("skill not found")

type errString string

func (e errString) Error() string {
	return string(e)
}

func skillNames(items []einoskill.FrontMatter) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ","
		}
		out += item.Name
	}
	return out
}

type skillHubTestModel struct {
	models []string
}

func (m *skillHubTestModel) Generate(_ context.Context, _ []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.record(opts...)
	return schema.AssistantMessage("ok", nil), nil
}

func (m *skillHubTestModel) Stream(_ context.Context, _ []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.record(opts...)
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("ok", nil)}), nil
}

func (m *skillHubTestModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *skillHubTestModel) record(opts ...model.Option) {
	common := model.GetCommonOptions(nil, opts...)
	if common.Model == nil {
		m.models = append(m.models, "")
		return
	}
	m.models = append(m.models, *common.Model)
}

func (m *skillHubTestModel) lastModel() string {
	if len(m.models) == 0 {
		return ""
	}
	return m.models[len(m.models)-1]
}

func writeSkillFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
