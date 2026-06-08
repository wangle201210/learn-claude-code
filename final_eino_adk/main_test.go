package main

import (
	"context"
	"testing"
)

func TestIsManualCompact(t *testing.T) {
	cases := map[string]bool{
		"/compact":      true,
		"compact":       true,
		" COMPACT ":     true,
		"/Compact":      true,
		"":              false,
		"/model":        false,
		"/model simple": false,
		"compact now":   false,
		"/compact now":  false,
	}

	for input, want := range cases {
		if got := isManualCompact(input); got != want {
			t.Fatalf("isManualCompact(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestNewFallbackModelRequiresFallbackModelEnv(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "primary-model")
	t.Setenv("OPENAI_FALLBACK_MODEL", "")

	fallback, err := NewFallbackModel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fallback != nil {
		t.Fatal("fallback model should be nil without OPENAI_FALLBACK_MODEL")
	}
}

func TestNewFallbackModelValidatesSharedModelEnvWhenEnabled(t *testing.T) {
	t.Setenv("OPENAI_FALLBACK_MODEL", "fallback-model")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "primary-model")
	t.Setenv("OPENAI_BASE_URL", "https://example.test/v1")

	fallback, err := NewFallbackModel(context.Background())
	if err == nil {
		t.Fatal("fallback model should validate shared model env")
	}
	if fallback != nil {
		t.Fatal("fallback model should be nil when shared env is invalid")
	}
}

func TestPrepareQuerySkipsBlankInputWithoutNotifications(t *testing.T) {
	query, ok := prepareQuery("", nil)
	if ok {
		t.Fatal("blank input without notifications should not run agent")
	}
	if query != "" {
		t.Fatalf("query = %q, want empty", query)
	}
}

func TestPrepareQueryKeepsBlankInputWithNotifications(t *testing.T) {
	query, ok := prepareQuery("", []string{"done"})
	if !ok {
		t.Fatal("blank input with notifications should run agent")
	}
	if query != "done\n\n" {
		t.Fatalf("query = %q, want notification payload", query)
	}
}

func TestPrepareQueryKeepsUserInput(t *testing.T) {
	query, ok := prepareQuery("hello", nil)
	if !ok {
		t.Fatal("non-blank input should run agent")
	}
	if query != "hello" {
		t.Fatalf("query = %q, want hello", query)
	}
}

func TestModelRouterConfigFromEnv(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "complex")
	t.Setenv("FINAL_EINO_SIMPLE_MODEL", "simple")
	t.Setenv("OPENAI_SIMPLE_MODEL", "ignored")
	t.Setenv("ANTHROPIC_SMALL_FAST_MODEL", "")
	t.Setenv("ANTHROPIC_DEFAULT_HAIKU_MODEL", "")
	t.Setenv("FINAL_EINO_STANDARD_MODEL", "")
	t.Setenv("OPENAI_STANDARD_MODEL", "")
	t.Setenv("ANTHROPIC_DEFAULT_SONNET_MODEL", "")
	t.Setenv("FINAL_EINO_COMPLEX_MODEL", "")
	t.Setenv("OPENAI_COMPLEX_MODEL", "")
	t.Setenv("ANTHROPIC_DEFAULT_OPUS_MODEL", "")
	t.Setenv("FINAL_EINO_MODEL_ROUTING", "")

	cfg := modelRouterConfigFromEnv()
	if cfg.simpleModel != "simple" {
		t.Fatalf("simpleModel = %q, want simple", cfg.simpleModel)
	}
	if cfg.complexModel != "complex" {
		t.Fatalf("complexModel = %q, want complex", cfg.complexModel)
	}
	if !cfg.enabled() {
		t.Fatal("routing should be enabled")
	}
}

func TestModelRouterConfigUsesAnthropicTierDefaults(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "openai-default")
	t.Setenv("FINAL_EINO_SIMPLE_MODEL", "")
	t.Setenv("OPENAI_SIMPLE_MODEL", "")
	t.Setenv("ANTHROPIC_SMALL_FAST_MODEL", "")
	t.Setenv("ANTHROPIC_DEFAULT_HAIKU_MODEL", "haiku-default")
	t.Setenv("FINAL_EINO_STANDARD_MODEL", "")
	t.Setenv("OPENAI_STANDARD_MODEL", "")
	t.Setenv("ANTHROPIC_DEFAULT_SONNET_MODEL", "sonnet-default")
	t.Setenv("FINAL_EINO_COMPLEX_MODEL", "")
	t.Setenv("OPENAI_COMPLEX_MODEL", "")
	t.Setenv("ANTHROPIC_DEFAULT_OPUS_MODEL", "opus-default")
	t.Setenv("FINAL_EINO_MODEL_ROUTING", "")

	cfg := modelRouterConfigFromEnv()
	if got := cfg.modelForTier(modelRouteSimple); got != "haiku-default" {
		t.Fatalf("simple tier = %q, want haiku-default", got)
	}
	if got := cfg.modelForTier(modelRouteStandard); got != "sonnet-default" {
		t.Fatalf("standard tier = %q, want sonnet-default", got)
	}
	if got := cfg.modelForTier(modelRouteComplex); got != "opus-default" {
		t.Fatalf("complex tier = %q, want opus-default", got)
	}
	if !cfg.enabled() {
		t.Fatal("routing should be enabled")
	}
}

func TestModelRouterSummaryDisabledWithoutSimpleModel(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "complex")
	t.Setenv("FINAL_EINO_SIMPLE_MODEL", "")
	t.Setenv("OPENAI_SIMPLE_MODEL", "")
	t.Setenv("ANTHROPIC_SMALL_FAST_MODEL", "")
	t.Setenv("ANTHROPIC_DEFAULT_HAIKU_MODEL", "")
	t.Setenv("FINAL_EINO_STANDARD_MODEL", "")
	t.Setenv("OPENAI_STANDARD_MODEL", "")
	t.Setenv("ANTHROPIC_DEFAULT_SONNET_MODEL", "")
	t.Setenv("FINAL_EINO_COMPLEX_MODEL", "")
	t.Setenv("OPENAI_COMPLEX_MODEL", "")
	t.Setenv("ANTHROPIC_DEFAULT_OPUS_MODEL", "")

	if got := modelRoutingSummaryFromEnv(); got != "" {
		t.Fatalf("summary = %q, want empty", got)
	}
}
