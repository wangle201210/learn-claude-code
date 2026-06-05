package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const memoryCacheTTL = 5 * time.Second

type memoryFile struct {
	filename    string
	name        string
	description string
	typ         string
	body        string
}

type memorySnapshot struct {
	index    string
	files    []memoryFile
	contents map[string]string
}

var storeCache = struct {
	mu        sync.Mutex
	expiresAt time.Time
	snapshot  memorySnapshot
}{}

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	if s == "" {
		s = "memory"
	}
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "/", "-")
	return s
}

func writeMemoryFile(name, typ, description, body string) {
	_ = os.MkdirAll(memoryDir, 0o755)
	path := filepath.Join(memoryDir, slugify(name)+".md")
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\ntype: %s\n---\n\n%s\n", name, description, typ, body)
	_ = os.WriteFile(path, []byte(content), 0o644)
	invalidateMemoryCache()
	rebuildIndex()
}

func rebuildIndex() {
	invalidateMemoryCache()
	files := listMemoryFiles()
	var b strings.Builder
	for _, f := range files {
		fmt.Fprintf(&b, "- [%s](%s) - %s\n", f.name, f.filename, f.description)
	}
	_ = os.MkdirAll(memoryDir, 0o755)
	_ = os.WriteFile(memoryIndex, []byte(b.String()), 0o644)
	invalidateMemoryCache()
}

func readMemoryIndex() string {
	return getMemorySnapshot().index
}

func readMemoryFile(filename string) (string, bool) {
	content, ok := getMemorySnapshot().contents[filename]
	return content, ok
}

func listMemoryFiles() []memoryFile {
	return cloneMemoryFiles(getMemorySnapshot().files)
}

func getMemorySnapshot() memorySnapshot {
	now := time.Now()
	storeCache.mu.Lock()
	defer storeCache.mu.Unlock()
	if now.Before(storeCache.expiresAt) {
		return cloneMemorySnapshot(storeCache.snapshot)
	}
	storeCache.snapshot = loadMemorySnapshotFromDisk()
	storeCache.expiresAt = now.Add(memoryCacheTTL)
	return cloneMemorySnapshot(storeCache.snapshot)
}

func invalidateMemoryCache() {
	storeCache.mu.Lock()
	defer storeCache.mu.Unlock()
	storeCache.expiresAt = time.Time{}
	storeCache.snapshot = memorySnapshot{}
}

func warmMemorySnapshot() {
	_ = getMemorySnapshot()
}

func loadMemorySnapshotFromDisk() memorySnapshot {
	snapshot := memorySnapshot{
		contents: map[string]string{},
	}
	if data, err := os.ReadFile(memoryIndex); err == nil {
		snapshot.index = strings.TrimSpace(string(data))
	}

	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		return snapshot
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && e.Name() != "MEMORY.md" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, fn := range names {
		raw, err := os.ReadFile(filepath.Join(memoryDir, fn))
		if err != nil {
			continue
		}
		text := string(raw)
		snapshot.contents[fn] = text
		meta, body := parseSimpleFrontmatter(text)
		name := meta["name"]
		if name == "" {
			name = strings.TrimSuffix(fn, ".md")
		}
		snapshot.files = append(snapshot.files, memoryFile{
			filename:    fn,
			name:        name,
			description: meta["description"],
			typ:         orDefault(meta["type"], "user"),
			body:        body,
		})
	}
	return snapshot
}

func cloneMemorySnapshot(snapshot memorySnapshot) memorySnapshot {
	return memorySnapshot{
		index:    snapshot.index,
		files:    cloneMemoryFiles(snapshot.files),
		contents: cloneStringMap(snapshot.contents),
	}
}

func cloneMemoryFiles(files []memoryFile) []memoryFile {
	if len(files) == 0 {
		return nil
	}
	out := make([]memoryFile, len(files))
	copy(out, files)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func parseSimpleFrontmatter(text string) (map[string]string, string) {
	meta := map[string]string{}
	if !strings.HasPrefix(text, "---") {
		return meta, text
	}
	parts := strings.SplitN(text, "---", 3)
	if len(parts) < 3 {
		return meta, text
	}
	for _, line := range strings.Split(strings.TrimSpace(parts[1]), "\n") {
		if i := strings.Index(line, ":"); i >= 0 {
			k := strings.TrimSpace(line[:i])
			v := strings.Trim(strings.TrimSpace(line[i+1:]), `"'`)
			meta[k] = v
		}
	}
	return meta, strings.TrimSpace(parts[2])
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
