package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	adkfs "github.com/cloudwego/eino/adk/filesystem"
)

func TestGrepRawDropsMalformedFileType(t *testing.T) {
	oldWorkdir := workdir
	t.Cleanup(func() { workdir = oldWorkdir })
	workdir = t.TempDir()

	fake := &grepCaptureBackend{}
	backend := &Backend{Backend: fake}

	_, err := backend.GrepRaw(context.Background(), &adkfs.GrepRequest{
		Pattern:  "needle",
		Path:     ".",
		FileType: ":DN-NeInNotesneConfSNoN?rsDLNotesS",
	})
	if err != nil {
		t.Fatalf("GrepRaw returned error: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("GrepRaw called backend %d times, want 1", fake.calls)
	}
	if fake.last.FileType != "" {
		t.Fatalf("FileType = %q, want empty", fake.last.FileType)
	}
	if fake.last.Path != workdir {
		t.Fatalf("Path = %q, want %q", fake.last.Path, workdir)
	}
}

func TestGrepRawRetriesWithoutUnrecognizedFileType(t *testing.T) {
	oldWorkdir := workdir
	t.Cleanup(func() { workdir = oldWorkdir })
	workdir = t.TempDir()

	fake := &grepCaptureBackend{firstErr: errors.New("ripgrep failed with exit code 2: rg: unrecognized file type: bogus")}
	backend := &Backend{Backend: fake}

	_, err := backend.GrepRaw(context.Background(), &adkfs.GrepRequest{
		Pattern:  "needle",
		Path:     ".",
		FileType: "bogus",
	})
	if err != nil {
		t.Fatalf("GrepRaw returned error after retry: %v", err)
	}
	if fake.calls != 2 {
		t.Fatalf("GrepRaw called backend %d times, want 2", fake.calls)
	}
	if fake.first.FileType != "bogus" {
		t.Fatalf("first FileType = %q, want bogus", fake.first.FileType)
	}
	if fake.last.FileType != "" {
		t.Fatalf("retry FileType = %q, want empty", fake.last.FileType)
	}
}

func TestSanitizeGrepFileType(t *testing.T) {
	cases := map[string]string{
		"go":        "go",
		" tsx ":     "tsx",
		"c++":       "",
		"bad/value": "",
		"":          "",
	}

	for input, want := range cases {
		if got := sanitizeGrepFileType(input); got != want {
			t.Fatalf("sanitizeGrepFileType(%q) = %q, want %q", input, got, want)
		}
	}
}

type grepCaptureBackend struct {
	adkfs.Backend
	calls    int
	first    adkfs.GrepRequest
	last     adkfs.GrepRequest
	firstErr error
}

func (b *grepCaptureBackend) GrepRaw(_ context.Context, req *adkfs.GrepRequest) ([]adkfs.GrepMatch, error) {
	b.calls++
	b.last = *req
	if b.calls == 1 {
		b.first = *req
		if b.firstErr != nil {
			return nil, b.firstErr
		}
	}
	return []adkfs.GrepMatch{{Path: filepath.Join(workdir, "file.go"), Line: 1, Content: "needle"}}, nil
}
