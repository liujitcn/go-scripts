package redis

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/redis/go-redis/v9"

	"github.com/liujitcn/go-scripts/source"
)

type Reader struct {
	client redisCmdable
	closer io.Closer

	prefix string

	mu     sync.RWMutex
	values map[string]string // key -> last loaded value (for change detection)
}

// 编译期断言：实现满足目标接口。
var _ source.Reader = (*Reader)(nil)

// 编译期断言：实现满足目标接口。
var _ source.ReadWatcher = (*Reader)(nil)

func withClient(c redisCmdable, closer io.Closer) Option {
	return func(o *configOptions) {
		o.client = c
		o.closer = closer
	}
}

func New(_ context.Context, opts ...Option) (*Reader, error) {
	cfg := &configOptions{
		addr: "localhost:6379",
		db:   0,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.client != nil {
		return &Reader{
			client: cfg.client,
			closer: cfg.closer,
			prefix: cfg.prefix,
			values: make(map[string]string),
		}, nil
	}

	client := redis.NewClient(&redis.Options{
		Addr:     cfg.addr,
		Password: cfg.password,
		Username: cfg.username,
		DB:       cfg.db,
	})

	return &Reader{
		client: client,
		closer: client,
		prefix: cfg.prefix,
		values: make(map[string]string),
	}, nil
}

func (r *Reader) resolveKey(key string) string {
	return r.prefix + key
}

func (r *Reader) Load(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	redisKey := r.resolveKey(key)
	val, err := r.client.Get(ctx, redisKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, redisKey)
		}
		return "", fmt.Errorf("redis source: get %q: %w", redisKey, err)
	}

	r.mu.Lock()
	r.values[key] = val
	r.mu.Unlock()

	return val, nil
}

func (r *Reader) Close() error {
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}

func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

func (r *Reader) hasValueChanged(ctx context.Context, key string) bool {
	redisKey := r.resolveKey(key)
	val, err := r.client.Get(ctx, redisKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {

			r.mu.RLock()
			_, existed := r.values[key]
			r.mu.RUnlock()
			return existed
		}
		return false // can't determine; don't signal.
	}

	r.mu.RLock()
	stored := r.values[key]
	r.mu.RUnlock()
	return val != stored
}
