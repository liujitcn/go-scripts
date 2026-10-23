package source

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// CachedSource 为远程 Reader 增加 MemSource 内存缓存，并可按 TTL 或 Watch 通知失效。
type CachedSource struct {
	remote Reader
	cache  *MemSource

	mu       sync.RWMutex
	loaded   map[string]struct{}           // 至少成功加载过一次的 key。
	watchers map[string]context.CancelFunc // 活跃监听的取消函数。

	// ttl 是缓存条目的存活时间；零值表示仅依赖 Watch 或手动失效。
	ttl time.Duration

	// ts 记录每个 key 最近一次从远程源获取的时间。
	tsMu sync.Mutex
	ts   map[string]time.Time
}

// 编译期断言：实现满足目标接口。
var _ Reader = (*CachedSource)(nil)

// 编译期断言：实现满足目标接口。
var _ ReadWatcher = (*CachedSource)(nil)

// CachedOption 配置 CachedSource。
type CachedOption func(*CachedSource)

// WithTTL 设置缓存条目的存活时间；零值关闭 TTL 过期。
func WithTTL(ttl time.Duration) CachedOption {
	return func(c *CachedSource) { c.ttl = ttl }
}

// NewCachedSource 为 remote 创建缓存包装；remote 不能为 nil。
func NewCachedSource(remote Reader, opts ...CachedOption) (*CachedSource, error) {
	if remote == nil {
		return nil, fmt.Errorf("cached source: remote reader is nil")
	}
	c := &CachedSource{
		remote:   remote,
		cache:    NewMemSource(),
		loaded:   make(map[string]struct{}),
		watchers: make(map[string]context.CancelFunc),
		ts:       make(map[string]time.Time),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Load 优先返回未过期缓存，否则读取远程源并更新缓存。
func (c *CachedSource) Load(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// 快速路径：先检查缓存。
	if c.isFresh(key) {
		if code, err := c.cache.Load(ctx, key); err == nil {
			return code, nil
		}
	}

	// 慢速路径：访问远程源。
	code, err := c.remote.Load(ctx, key)
	if err != nil {
		return "", fmt.Errorf("cached source: remote load %q: %w", key, err)
	}

	// 写入缓存。
	c.cache.Set(key, code)
	c.tsMu.Lock()
	c.ts[key] = time.Now()
	c.tsMu.Unlock()

	c.mu.Lock()
	if _, exists := c.loaded[key]; !exists {
		c.loaded[key] = struct{}{}
		// 远程源支持监听时启动后台监听。
		c.startWatcherLocked(key)
	}
	c.mu.Unlock()

	return code, nil
}

// Close 停止后台监听并关闭远程脚本源。
func (c *CachedSource) Close() error {
	c.mu.Lock()
	for key, cancel := range c.watchers {
		cancel()
		delete(c.watchers, key)
	}
	c.mu.Unlock()
	return c.remote.Close()
}

// Invalidate 删除 key 的缓存，强制下一次 Load 访问远程源。
func (c *CachedSource) Invalidate(key string) {
	c.cache.Delete(key)
	c.tsMu.Lock()
	delete(c.ts, key)
	c.tsMu.Unlock()
}

// InvalidateAll 清空全部缓存。
func (c *CachedSource) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key := range c.loaded {
		c.cache.Delete(key)
	}
	c.tsMu.Lock()
	c.ts = make(map[string]time.Time)
	c.tsMu.Unlock()
}

// isFresh 判断 key 的缓存是否存在且未超过 TTL。
func (c *CachedSource) isFresh(key string) bool {
	c.mu.RLock()
	_, loaded := c.loaded[key]
	c.mu.RUnlock()
	if !loaded {
		return false
	}

	if c.ttl > 0 {
		c.tsMu.Lock()
		ts, ok := c.ts[key]
		c.tsMu.Unlock()
		if !ok || time.Since(ts) > c.ttl {
			return false
		}
	}

	return true
}

// startWatcherLocked 在持有写锁时为 key 启动远程 Watch 监听。
func (c *CachedSource) startWatcherLocked(key string) {
	w, ok := c.remote.(Watcher)
	if !ok {
		return // 远程源不支持监听，只依赖 TTL 或手动失效。
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.watchers[key] = cancel

	go func() {
		ch, err := w.Watch(ctx, key)
		if err != nil {
			// 监听建立失败时保留缓存，后续依赖 TTL 或手动失效。
			return
		}
		for range ch {
			c.cache.Delete(key)
			c.tsMu.Lock()
			delete(c.ts, key)
			c.tsMu.Unlock()
		}
	}()
}

// Watch 将监听请求转发给远程脚本源。
func (c *CachedSource) Watch(ctx context.Context, key string) (<-chan struct{}, error) {
	w, ok := c.remote.(Watcher)
	if !ok {
		return nil, fmt.Errorf("cached source: remote %T does not implement Watcher", c.remote)
	}
	return w.Watch(ctx, key)
}
