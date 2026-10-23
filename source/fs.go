package source

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
)

// FSSourceOption 配置 FileSystemSource。
type FSSourceOption func(*FileSystemSource)

// WithFSPrefix 设置读取 fs.FS 时追加到 key 前的路径前缀。
// 前缀开头的斜杠会移除，末尾缺少斜杠时自动补充。
func WithFSPrefix(prefix string) FSSourceOption {
	return func(s *FileSystemSource) {
		p := strings.TrimPrefix(prefix, "/")
		if p != "" && !strings.HasSuffix(p, "/") {
			p += "/"
		}
		s.prefix = p
	}
}

// FileSystemSource 从任意 fs.FS 实现读取脚本，不负责监听文件变化。
type FileSystemSource struct {
	fsys   fs.FS
	prefix string
}

// NewFileSystemSource 使用 fsys 创建文件系统脚本源；fsys 为 nil 时返回错误。
func NewFileSystemSource(fsys fs.FS, opts ...FSSourceOption) (*FileSystemSource, error) {
	if fsys == nil {
		return nil, fmt.Errorf("fs source: underlying fs.FS is nil")
	}
	s := &FileSystemSource{fsys: fsys}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Load 读取 prefix + key 对应的 fs.FS 文件。
func (s *FileSystemSource) Load(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// fs.FS 路径不能以斜杠开头。
	key = strings.TrimPrefix(key, "/")
	fullKey := s.prefix + key

	b, err := fs.ReadFile(s.fsys, fullKey)
	if err != nil {
		return "", fmt.Errorf("fs source: read %q: %w", fullKey, err)
	}
	return string(b), nil
}

// Close 无需执行任何操作，底层 fs.FS 由调用方管理。
func (s *FileSystemSource) Close() error { return nil }

// 编译期断言：实现满足目标接口。
var _ Reader = (*FileSystemSource)(nil)
