package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectAgentInstructionFilesIncludesClaudeAndRules(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "services", "api")
	for _, dir := range []string{
		filepath.Join(root, ".claude", "rules"),
		filepath.Join(root, "services", ".claude", "rules"),
		filepath.Join(cwd, ".claude", "rules"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		filepath.Join(root, "CLAUDE.md"),
		filepath.Join(root, ".claude", "CLAUDE.md"),
		filepath.Join(root, ".claude", "rules", "b.md"),
		filepath.Join(root, ".claude", "rules", "a.md"),
		filepath.Join(root, "CLAUDE.local.md"),
		filepath.Join(root, "services", "CLAUDE.md"),
		filepath.Join(root, "services", ".claude", "rules", "svc.md"),
		filepath.Join(cwd, ".claude", "CLAUDE.md"),
		filepath.Join(cwd, ".claude", "rules", "api.md"),
		filepath.Join(cwd, "CLAUDE.local.md"),
	} {
		writeTestFile(t, path)
	}

	got, err := collectAgentInstructionFiles(root, cwd)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		filepath.Join(root, "CLAUDE.md"),
		filepath.Join(root, ".claude", "CLAUDE.md"),
		filepath.Join(root, ".claude", "rules", "a.md"),
		filepath.Join(root, ".claude", "rules", "b.md"),
		filepath.Join(root, "CLAUDE.local.md"),
		filepath.Join(root, "services", "CLAUDE.md"),
		filepath.Join(root, "services", ".claude", "rules", "svc.md"),
		filepath.Join(cwd, ".claude", "CLAUDE.md"),
		filepath.Join(cwd, ".claude", "rules", "api.md"),
		filepath.Join(cwd, "CLAUDE.local.md"),
	}
	assertStringSlice(t, got, want)
}

func TestCollectAgentInstructionFilesFallsBackWhenCwdEscapesRoot(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"))
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(outside, "CLAUDE.md"))

	got, err := collectAgentInstructionFiles(root, outside)
	if err != nil {
		t.Fatal(err)
	}

	assertStringSlice(t, got, []string{filepath.Join(root, "CLAUDE.md")})
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(filepath.Base(path)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertStringSlice(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d files, want %d\ngot:  %#v\nwant: %#v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("file %d = %q, want %q\ngot:  %#v\nwant: %#v", i, got[i], want[i], got, want)
		}
	}
}
