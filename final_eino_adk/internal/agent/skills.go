package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/adk"
	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/permission"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/workspace"
)

type compositeSkillBackend struct {
	backends []einoskill.Backend
}

type skillAgentHub struct {
	primary          model.ToolCallingChatModel
	workspaceBackend *workspace.Backend
	prompt           permission.PromptFunc
}

type skillModelHub struct {
	primary model.ToolCallingChatModel
}

func buildSkillBackend(ctx context.Context, backend *workspace.Backend, root string) (einoskill.Backend, error) {
	var backends []einoskill.Backend
	metadata := map[string]claudeSkillMetadata{}
	for _, dir := range skillDirs(root) {
		if _, err := os.Stat(dir); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat skill dir %s: %w", dir, err)
		}
		nextMetadata, err := loadClaudeSkillMetadata(dir)
		if err != nil {
			return nil, err
		}
		for name, item := range nextMetadata {
			metadata[name] = item
		}
		skillBackend, err := einoskill.NewBackendFromFilesystem(ctx, &einoskill.BackendFromFilesystemConfig{
			Backend: backend,
			BaseDir: dir,
		})
		if err != nil {
			return nil, err
		}
		backends = append(backends, skillBackend)
	}
	if len(backends) == 0 {
		return nil, nil
	}
	var skillBackend einoskill.Backend
	if len(backends) == 1 {
		skillBackend = backends[0]
	} else {
		skillBackend = &compositeSkillBackend{backends: backends}
	}
	if len(metadata) == 0 {
		return skillBackend, nil
	}
	return &claudeSkillBackend{base: skillBackend, metadata: metadata}, nil
}

func skillDirs(root string) []string {
	return []string{
		filepath.Join(root, "skills"),
		filepath.Join(root, ".claude", "skills"),
	}
}

func (b *compositeSkillBackend) List(ctx context.Context) ([]einoskill.FrontMatter, error) {
	merged := map[string]einoskill.FrontMatter{}
	var order []string
	for _, backend := range b.backends {
		items, err := backend.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			name := item.Name
			if name == "" {
				continue
			}
			if _, ok := merged[name]; !ok {
				order = append(order, name)
			}
			merged[name] = item
		}
	}
	out := make([]einoskill.FrontMatter, 0, len(order))
	for _, name := range order {
		out = append(out, merged[name])
	}
	return out, nil
}

func (b *compositeSkillBackend) Get(ctx context.Context, name string) (einoskill.Skill, error) {
	for i := len(b.backends) - 1; i >= 0; i-- {
		s, err := b.backends[i].Get(ctx, name)
		if err == nil {
			return s, nil
		}
	}
	return einoskill.Skill{}, fmt.Errorf("skill not found: %s", name)
}

func newSkillAgentHub(primary model.ToolCallingChatModel, workspaceBackend *workspace.Backend, prompt permission.PromptFunc) *skillAgentHub {
	return &skillAgentHub{
		primary:          primary,
		workspaceBackend: workspaceBackend,
		prompt:           prompt,
	}
}

func (h *skillAgentHub) Get(ctx context.Context, name string, opts *einoskill.AgentHubOptions) (adk.TypedAgent[*schema.Message], error) {
	if h == nil || h.primary == nil {
		return nil, fmt.Errorf("primary model is required")
	}
	agentName := strings.TrimSpace(name)
	if agentName == "" {
		agentName = "skill_agent"
	}
	modelOverride := model.BaseChatModel(nil)
	if opts != nil {
		modelOverride = opts.Model
	}
	return buildDelegateAgent(
		ctx,
		agentName,
		"Run a forked skill agent.",
		"You are a skill agent. Execute the loaded skill instructions with the provided context. Return concise results, changed files, and remaining risks. Do not delegate further.",
		h.primary,
		modelOverride,
		h.workspaceBackend,
		h.prompt,
	)
}

func newSkillModelHub(primary model.ToolCallingChatModel) *skillModelHub {
	return &skillModelHub{primary: primary}
}

func (h *skillModelHub) Get(_ context.Context, name string) (model.BaseChatModel, error) {
	if h == nil || h.primary == nil {
		return nil, fmt.Errorf("primary model is required")
	}
	modelName := resolveSkillModelName(name)
	if modelName == "" {
		return h.primary, nil
	}
	return &fixedModelOption{
		base:      h.primary,
		modelName: modelName,
	}, nil
}

type fixedModelOption struct {
	base      model.BaseChatModel
	modelName string
}

func (m *fixedModelOption) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return m.base.Generate(ctx, input, appendFixedModelOption(opts, m.modelName)...)
}

func (m *fixedModelOption) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.base.Stream(ctx, input, appendFixedModelOption(opts, m.modelName)...)
}

func appendFixedModelOption(opts []model.Option, modelName string) []model.Option {
	if modelName == "" || model.GetCommonOptions(nil, opts...).Model != nil {
		return opts
	}
	out := make([]model.Option, 0, len(opts)+1)
	out = append(out, opts...)
	out = append(out, model.WithModel(modelName))
	return out
}

func resolveSkillModelName(name string) string {
	raw := strings.TrimSpace(name)
	if raw == "" {
		return ""
	}
	switch strings.ToLower(raw) {
	case "inherit":
		return ""
	case "haiku", "small", "fast", "simple":
		return firstNonEmptyAgentEnv(
			"FINAL_EINO_SIMPLE_MODEL",
			"OPENAI_SIMPLE_MODEL",
			"ANTHROPIC_SMALL_FAST_MODEL",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL",
		)
	case "sonnet", "standard":
		return firstNonEmptyAgentEnv(
			"FINAL_EINO_STANDARD_MODEL",
			"OPENAI_STANDARD_MODEL",
			"ANTHROPIC_DEFAULT_SONNET_MODEL",
			"OPENAI_MODEL",
		)
	case "opus", "best", "complex":
		return firstNonEmptyAgentEnv(
			"FINAL_EINO_COMPLEX_MODEL",
			"OPENAI_COMPLEX_MODEL",
			"ANTHROPIC_DEFAULT_OPUS_MODEL",
			"OPENAI_MODEL",
		)
	default:
		return raw
	}
}

func firstNonEmptyAgentEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
