package modelenv

import (
	"strings"
	"testing"
)

func TestRequireReportsMissingVars(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", " ")
	t.Setenv("OPENAI_BASE_URL", "https://example.test/v1")

	err := Require()
	if err == nil {
		t.Fatal("Require should report missing env vars")
	}
	msg := err.Error()
	for _, want := range []string{"OPENAI_API_KEY", "OPENAI_MODEL", "export OPENAI_BASE_URL"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q should contain %q", msg, want)
		}
	}
	if strings.Contains(msg, "OPENAI_BASE_URL,") {
		t.Fatalf("error %q should not report configured OPENAI_BASE_URL as missing", msg)
	}
}

func TestRequireAcceptsConfiguredVars(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "key")
	t.Setenv("OPENAI_MODEL", "model")
	t.Setenv("OPENAI_BASE_URL", "https://example.test/v1")

	if err := Require(); err != nil {
		t.Fatalf("Require returned %v", err)
	}
}
