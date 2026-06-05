package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var workdir = findWorkspaceRoot(mustGetwd())

func Dir() string {
	return workdir
}

func mustGetwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func findWorkspaceRoot(cwd string) string {
	for dir := filepath.Clean(cwd); ; dir = filepath.Dir(dir) {
		if isRepoWorkspaceRoot(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Clean(cwd)
		}
	}
}

func isRepoWorkspaceRoot(dir string) bool {
	required := []string{"go.mod", "final_eino_adk", "s01_agent_loop", "s19_mcp_plugin"}
	for _, name := range required {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
}

// SafePath keeps all filesystem middleware access under the current workspace.
func SafePath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		p = "."
	}
	joined := p
	if !filepath.IsAbs(joined) {
		joined = filepath.Join(workdir, joined)
	}
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	if isInsideWorkspace(abs) {
		return abs, nil
	}
	if mapped, ok := remapWorkspaceAlias(abs); ok {
		return mapped, nil
	}
	return "", fmt.Errorf("path escapes workspace: %s", p)
}

func isInsideWorkspace(abs string) bool {
	rel, err := filepath.Rel(workdir, abs)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func remapWorkspaceAlias(abs string) (string, bool) {
	workspaceName := filepath.Base(workdir)
	parts := strings.Split(strings.Trim(abs, string(filepath.Separator)), string(filepath.Separator))
	for i, part := range parts {
		if part != workspaceName {
			continue
		}
		candidate := workdir
		if i+1 < len(parts) {
			next := append([]string{workdir}, parts[i+1:]...)
			candidate = filepath.Join(next...)
		}
		candidateAbs, err := filepath.Abs(candidate)
		if err != nil || !isInsideWorkspace(candidateAbs) {
			continue
		}
		return candidateAbs, true
	}
	return "", false
}
