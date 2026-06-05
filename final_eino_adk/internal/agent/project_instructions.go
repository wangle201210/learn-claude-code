package agent

import (
	"os"
	"path/filepath"
	"sort"
)

func collectAgentInstructionFiles(root, cwd string) ([]string, error) {
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return nil, err
	}
	if cwd == "" {
		cwd = root
	}
	cwd, err = filepath.Abs(filepath.Clean(cwd))
	if err != nil {
		return nil, err
	}
	if !pathWithin(cwd, root) {
		cwd = root
	}

	dirs := instructionDirs(root, cwd)
	seen := map[string]bool{}
	var files []string
	addFile := func(path string) error {
		path = filepath.Clean(path)
		if seen[path] {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		seen[path] = true
		files = append(files, path)
		return nil
	}

	for _, dir := range dirs {
		if err := addFile(filepath.Join(dir, "CLAUDE.md")); err != nil {
			return nil, err
		}
		if err := addFile(filepath.Join(dir, ".claude", "CLAUDE.md")); err != nil {
			return nil, err
		}
		ruleFiles, err := filepath.Glob(filepath.Join(dir, ".claude", "rules", "*.md"))
		if err != nil {
			return nil, err
		}
		sort.Strings(ruleFiles)
		for _, ruleFile := range ruleFiles {
			if err := addFile(ruleFile); err != nil {
				return nil, err
			}
		}
		if err := addFile(filepath.Join(dir, "CLAUDE.local.md")); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func instructionDirs(root, cwd string) []string {
	var dirs []string
	for dir := cwd; pathWithin(dir, root); dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if dir == root {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}
	return dirs
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || rel != ".." && !hasDotDotPrefix(rel))
}

func hasDotDotPrefix(rel string) bool {
	return len(rel) > 3 && rel[:3] == ".."+string(filepath.Separator)
}
