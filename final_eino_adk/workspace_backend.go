package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	adkfs "github.com/cloudwego/eino/adk/filesystem"
	adkfsmw "github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/plantask"
)

type workspaceBackend struct {
	adkfs.Backend
	shell adkfs.Shell
}

func (b *workspaceBackend) LsInfo(ctx context.Context, req *adkfs.LsInfoRequest) ([]adkfs.FileInfo, error) {
	p, err := safePath(req.Path)
	if err != nil {
		return nil, err
	}
	files, err := b.Backend.LsInfo(ctx, &adkfs.LsInfoRequest{Path: p})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (b *workspaceBackend) Read(ctx context.Context, req *adkfs.ReadRequest) (*adkfs.FileContent, error) {
	p, err := safePath(req.FilePath)
	if err != nil {
		return nil, err
	}
	return b.Backend.Read(ctx, &adkfs.ReadRequest{
		FilePath: p,
		Offset:   req.Offset,
		Limit:    req.Limit,
	})
}

func (b *workspaceBackend) GrepRaw(ctx context.Context, req *adkfs.GrepRequest) ([]adkfs.GrepMatch, error) {
	path := req.Path
	if path == "" {
		path = "."
	}
	p, err := safePath(path)
	if err != nil {
		return nil, err
	}
	next := *req
	next.Path = p
	matches, err := b.Backend.GrepRaw(ctx, &next)
	if err != nil {
		return nil, err
	}
	return matches, nil
}

func (b *workspaceBackend) GlobInfo(ctx context.Context, req *adkfs.GlobInfoRequest) ([]adkfs.FileInfo, error) {
	path := req.Path
	if path == "" {
		path = "."
	}
	p, err := safePath(path)
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

func (b *workspaceBackend) Write(ctx context.Context, req *adkfs.WriteRequest) error {
	p, err := safePath(req.FilePath)
	if err != nil {
		return err
	}
	return b.Backend.Write(ctx, &adkfs.WriteRequest{
		FilePath: p,
		Content:  req.Content,
	})
}

func (b *workspaceBackend) Edit(ctx context.Context, req *adkfs.EditRequest) error {
	p, err := safePath(req.FilePath)
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

func (b *workspaceBackend) MultiModalRead(ctx context.Context, req *adkfs.MultiModalReadRequest) (*adkfs.MultiFileContent, error) {
	reader, ok := b.Backend.(adkfs.MultiModalReader)
	if !ok {
		content, err := b.Read(ctx, &req.ReadRequest)
		if err != nil {
			return nil, err
		}
		return &adkfs.MultiFileContent{FileContent: content}, nil
	}
	p, err := safePath(req.FilePath)
	if err != nil {
		return nil, err
	}
	next := *req
	next.FilePath = p
	return reader.MultiModalRead(ctx, &next)
}

func (b *workspaceBackend) Execute(ctx context.Context, input *adkfs.ExecuteRequest) (*adkfs.ExecuteResponse, error) {
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

type taskBackend struct {
	backend *workspaceBackend
}

func (b *taskBackend) LsInfo(ctx context.Context, req *plantask.LsInfoRequest) ([]plantask.FileInfo, error) {
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

func (b *taskBackend) Read(ctx context.Context, req *plantask.ReadRequest) (*adkfsmw.FileContent, error) {
	return b.backend.Read(ctx, (*adkfs.ReadRequest)(req))
}

func (b *taskBackend) Write(ctx context.Context, req *plantask.WriteRequest) error {
	return b.backend.Write(ctx, (*adkfs.WriteRequest)(req))
}

func (b *taskBackend) Delete(ctx context.Context, req *plantask.DeleteRequest) error {
	p, err := safePath(req.FilePath)
	if err != nil {
		return err
	}
	return os.Remove(p)
}
