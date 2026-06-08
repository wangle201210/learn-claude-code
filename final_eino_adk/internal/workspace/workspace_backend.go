package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/adk/backend/local"
	adkfs "github.com/cloudwego/eino/adk/filesystem"
	adkfsmw "github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/plantask"
)

type Backend struct {
	adkfs.Backend
	shell adkfs.Shell
}

var validGrepFileTypePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func New(ctx context.Context, validateCommand func(string) error) (*Backend, error) {
	localBackend, err := local.NewBackend(ctx, &local.Config{ValidateCommand: validateCommand})
	if err != nil {
		return nil, err
	}
	return &Backend{Backend: localBackend, shell: localBackend}, nil
}

func (b *Backend) LsInfo(ctx context.Context, req *adkfs.LsInfoRequest) ([]adkfs.FileInfo, error) {
	p, err := SafePath(req.Path)
	if err != nil {
		return nil, err
	}
	files, err := b.Backend.LsInfo(ctx, &adkfs.LsInfoRequest{Path: p})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (b *Backend) Read(ctx context.Context, req *adkfs.ReadRequest) (*adkfs.FileContent, error) {
	p, err := SafePath(req.FilePath)
	if err != nil {
		return nil, err
	}
	return b.Backend.Read(ctx, &adkfs.ReadRequest{
		FilePath: p,
		Offset:   req.Offset,
		Limit:    req.Limit,
	})
}

func (b *Backend) GrepRaw(ctx context.Context, req *adkfs.GrepRequest) ([]adkfs.GrepMatch, error) {
	path := req.Path
	if path == "" {
		path = "."
	}
	p, err := SafePath(path)
	if err != nil {
		return nil, err
	}
	next := *req
	next.Path = p
	next.FileType = sanitizeGrepFileType(next.FileType)
	matches, err := b.Backend.GrepRaw(ctx, &next)
	if err != nil {
		if next.FileType != "" && isUnrecognizedRipgrepFileTypeError(err) {
			next.FileType = ""
			return b.Backend.GrepRaw(ctx, &next)
		}
		return nil, err
	}
	return matches, nil
}

func sanitizeGrepFileType(fileType string) string {
	fileType = strings.TrimSpace(fileType)
	if !validGrepFileTypePattern.MatchString(fileType) {
		return ""
	}
	return fileType
}

func isUnrecognizedRipgrepFileTypeError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "unrecognized file type")
}

func (b *Backend) GlobInfo(ctx context.Context, req *adkfs.GlobInfoRequest) ([]adkfs.FileInfo, error) {
	path := req.Path
	if path == "" {
		path = "."
	}
	p, err := SafePath(path)
	if err != nil {
		return nil, err
	}
	files, err := b.Backend.GlobInfo(ctx, &adkfs.GlobInfoRequest{
		Pattern: req.Pattern,
		Path:    p,
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (b *Backend) Write(ctx context.Context, req *adkfs.WriteRequest) error {
	p, err := SafePath(req.FilePath)
	if err != nil {
		return err
	}
	return b.Backend.Write(ctx, &adkfs.WriteRequest{
		FilePath: p,
		Content:  req.Content,
	})
}

func (b *Backend) Edit(ctx context.Context, req *adkfs.EditRequest) error {
	p, err := SafePath(req.FilePath)
	if err != nil {
		return err
	}
	return b.Backend.Edit(ctx, &adkfs.EditRequest{
		FilePath:   p,
		OldString:  req.OldString,
		NewString:  req.NewString,
		ReplaceAll: req.ReplaceAll,
	})
}

func (b *Backend) MultiModalRead(ctx context.Context, req *adkfs.MultiModalReadRequest) (*adkfs.MultiFileContent, error) {
	reader, ok := b.Backend.(adkfs.MultiModalReader)
	if !ok {
		content, err := b.Read(ctx, &req.ReadRequest)
		if err != nil {
			return nil, err
		}
		return &adkfs.MultiFileContent{FileContent: content}, nil
	}
	p, err := SafePath(req.FilePath)
	if err != nil {
		return nil, err
	}
	next := *req
	next.FilePath = p
	return reader.MultiModalRead(ctx, &next)
}

func (b *Backend) Execute(ctx context.Context, input *adkfs.ExecuteRequest) (*adkfs.ExecuteResponse, error) {
	if b.shell == nil {
		return nil, errors.New("shell is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	resp, err := b.shell.Execute(ctx, input)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &adkfs.ExecuteResponse{Output: "Error: Timeout (120s)"}, nil
	}
	return resp, err
}

func (b *Backend) ExecuteBackground(ctx context.Context, input *adkfs.ExecuteRequest) (*adkfs.ExecuteResponse, error) {
	if b.shell == nil {
		return nil, errors.New("shell is not configured")
	}
	return b.shell.Execute(ctx, input)
}

type TaskBackend struct {
	backend *Backend
}

func NewTaskBackend(backend *Backend) *TaskBackend {
	return &TaskBackend{backend: backend}
}

func (b *TaskBackend) LsInfo(ctx context.Context, req *plantask.LsInfoRequest) ([]plantask.FileInfo, error) {
	files, err := b.backend.LsInfo(ctx, (*adkfs.LsInfoRequest)(req))
	if err != nil {
		return nil, err
	}
	for i := range files {
		if !filepath.IsAbs(files[i].Path) && filepath.Dir(files[i].Path) == "." {
			files[i].Path = filepath.Join(req.Path, files[i].Path)
		}
	}
	return files, nil
}

func (b *TaskBackend) Read(ctx context.Context, req *plantask.ReadRequest) (*adkfsmw.FileContent, error) {
	return b.backend.Read(ctx, (*adkfs.ReadRequest)(req))
}

func (b *TaskBackend) Write(ctx context.Context, req *plantask.WriteRequest) error {
	return b.backend.Write(ctx, (*adkfs.WriteRequest)(req))
}

func (b *TaskBackend) Delete(ctx context.Context, req *plantask.DeleteRequest) error {
	p, err := SafePath(req.FilePath)
	if err != nil {
		return err
	}
	return os.Remove(p)
}
