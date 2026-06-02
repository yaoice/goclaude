// Package rules 定义规则持久化接口
package rules

import "context"

// Repository 规则持久化接口
type Repository interface {
	ReadFile(ctx context.Context, path string) (string, error)
	ReadFileRange(ctx context.Context, path string, maxLines int) (content string, mtimeMs int64, err error)
	ReadDir(ctx context.Context, path string, recursive bool) ([]DirEntry, error)
	Stat(ctx context.Context, path string) (FileInfo, error)
	RealPath(ctx context.Context, path string) (string, error)
	Exists(ctx context.Context, path string) bool
	WriteFile(ctx context.Context, path string, content string) error
	MkdirAll(ctx context.Context, path string) error
}
