package source

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// FileSource 从本地文件系统读取脚本，并通过修改时间轮询支持 Watch。
type FileSource struct {
	mu     sync.RWMutex
	mtimes map[string]time.Time // 记录每个 key 最近一次加载时的文件修改时间。
}

// NewFileSource 创建空的本地文件脚本源。
func NewFileSource() *FileSource {
	return &FileSource{mtimes: make(map[string]time.Time)}
}

// Load 读取 key 指定文件并记录其修改时间。
func (fs *FileSource) Load(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	b, err := os.ReadFile(key)
	if err != nil {
		return "", fmt.Errorf("file source: read %q: %w", key, err)
	}
	if fi, statErr := os.Stat(key); statErr == nil {
		fs.mu.Lock()
		fs.mtimes[key] = fi.ModTime()
		fs.mu.Unlock()
	}
	return string(b), nil
}

// Close 无需执行任何操作，FileSource 不持有外部资源。
func (fs *FileSource) Close() error { return nil }

// 编译期断言：实现满足目标接口。
var _ ReadWatcher = (*FileSource)(nil)

// Watch 每秒轮询文件修改时间，变化时发送通知，ctx 取消时关闭通道。
func (fs *FileSource) Watch(ctx context.Context, key string) (<-chan struct{}, error) {
	ch := make(chan struct{})

	// 记录初始修改时间。
	fi, err := os.Stat(key)
	if err != nil {
		return nil, fmt.Errorf("file source: stat %q: %w", key, err)
	}
	lastMod := fi.ModTime()

	go func() {
		defer close(ch)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fi, err := os.Stat(key)
				if err != nil {
					// 文件已删除或暂时不可访问，跳过本次轮询。
					continue
				}
				if fi.ModTime().After(lastMod) {
					lastMod = fi.ModTime()
					// 非阻塞发送，避免接收方退出后监听协程泄漏。
					select {
					case ch <- struct{}{}:
					default:
					}
				}
			}
		}
	}()

	return ch, nil
}
