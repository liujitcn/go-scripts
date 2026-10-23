package source

import (
	"context"
	"fmt"
	"sync"
)

type MemSource struct {
	mu       sync.Mutex
	data     map[string]string
	watchers map[string][]chan struct{} // key -> list of watch channels
}

func NewMemSource() *MemSource {
	return &MemSource{
		data:     make(map[string]string),
		watchers: make(map[string][]chan struct{}),
	}
}

func (ms *MemSource) Set(key, code string) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.data[key] = code

	if chs, ok := ms.watchers[key]; ok {
		for _, ch := range chs {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

func (ms *MemSource) Delete(key string) {
	ms.mu.Lock()
	delete(ms.data, key)

	if chs, ok := ms.watchers[key]; ok {
		for _, ch := range chs {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
	ms.mu.Unlock()
}

func (ms *MemSource) Load(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	code, ok := ms.data[key]
	if !ok {
		return "", fmt.Errorf("mem source: key %q not found", key)
	}
	return code, nil
}

func (ms *MemSource) Close() error { return nil }

// 编译期断言：实现满足目标接口。
var _ ReadWatcher = (*MemSource)(nil)

//

func (ms *MemSource) Watch(ctx context.Context, key string) (<-chan struct{}, error) {
	ch := make(chan struct{}, 1) // buffered to avoid missing notifications

	ms.mu.Lock()
	ms.watchers[key] = append(ms.watchers[key], ch)
	ms.mu.Unlock()

	go func() {
		<-ctx.Done()
		// 从监听表移除通道，避免内存泄漏。

		ms.mu.Lock()
		defer ms.mu.Unlock()
		if chs, ok := ms.watchers[key]; ok {
			for i, c := range chs {
				if c == ch {
					ms.watchers[key] = append(chs[:i], chs[i+1:]...)
					break
				}
			}
		}
		close(ch)
	}()

	return ch, nil
}
