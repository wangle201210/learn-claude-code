package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafePathRemapsWorkspaceAlias(t *testing.T) {
	oldWorkdir := workdir
	t.Cleanup(func() { workdir = oldWorkdir })

	root := t.TempDir()
	workdir = filepath.Join(root, "owner", "learn-claude-code")

	alias := filepath.Join(root, "learn-claude-code", "final_eino_adk")
	got, err := SafePath(alias)
	if err != nil {
		t.Fatalf("SafePath(%q) returned error: %v", alias, err)
	}

	want := filepath.Join(workdir, "final_eino_adk")
	if got != want {
		t.Fatalf("SafePath(%q) = %q, want %q", alias, got, want)
	}
}

func TestSafePathRejectsUnrelatedAbsolutePath(t *testing.T) {
	oldWorkdir := workdir
	t.Cleanup(func() { workdir = oldWorkdir })

	root := t.TempDir()
	workdir = filepath.Join(root, "owner", "learn-claude-code")

	_, err := SafePath(filepath.Join(root, "other-repo"))
	if err == nil {
		t.Fatal("SafePath accepted unrelated absolute path")
	}
}

func TestFindWorkspaceRootFromFinalEinoAdkDir(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "owner", "learn-claude-code")
	for _, name := range []string{"final_eino_adk", "s01_agent_loop", "s19_mcp_plugin"} {
		if err := mkdirAll(filepath.Join(repo, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeFile(filepath.Join(repo, "go.mod"), "module test\n"); err != nil {
		t.Fatal(err)
	}

	got := findWorkspaceRoot(filepath.Join(repo, "final_eino_adk"))
	if got != repo {
		t.Fatalf("findWorkspaceRoot returned %q, want %q", got, repo)
	}
}

func mkdirAll(path string) error {
	return os.MkdirAll(path, 0o755)
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
