package main

import (
	"context"
	"testing"
)

func TestIsManualCompact(t *testing.T) {
	cases := map[string]bool{
		"/compact":     true,
		"compact":      true,
		" COMPACT ":    true,
		"/Compact":     true,
		"":             false,
		"compact now":  false,
		"/compact now": false,
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
	t.Setenv("FINAL_EINO_COMPLEX_MODEL", "")
	t.Setenv("OPENAI_COMPLEX_MODEL", "")
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

func TestModelRouterSummaryDisabledWithoutSimpleModel(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "complex")
	t.Setenv("FINAL_EINO_SIMPLE_MODEL", "")
	t.Setenv("OPENAI_SIMPLE_MODEL", "")
	t.Setenv("ANTHROPIC_SMALL_FAST_MODEL", "")

	if got := modelRoutingSummaryFromEnv(); got != "" {
		t.Fatalf("summary = %q, want empty", got)
	}
}
