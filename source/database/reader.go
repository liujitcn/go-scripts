package database

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/liujitcn/go-scripts/source"
)

type Reader struct {
	api     dbAPI // database operations (real or fake)
	prefix  string
	ownsAPI bool // true if this Reader should close the API on Close()

	pollInterval time.Duration

	mu        sync.RWMutex
	values    map[string]string // key -> last loaded value
	checksums map[string]string // key -> last checksum (for change detection)
	closed    bool
}

// 编译期断言：实现满足目标接口。
var _ source.Reader = (*Reader)(nil)

// 编译期断言：实现满足目标接口。
var _ source.ReadWatcher = (*Reader)(nil)

const defaultPollInterval = 10 * time.Second

func withAPI(api dbAPI) Option {
	return func(o *configOptions) {
		o.api = api
	}
}

func New(_ context.Context, opts ...Option) (*Reader, error) {
	cfg := &configOptions{
		pollInterval: defaultPollInterval,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.api != nil {
		return &Reader{
			api:          cfg.api,
			prefix:       cfg.prefix,
			ownsAPI:      false,
			pollInterval: cfg.pollInterval,
			values:       make(map[string]string),
			checksums:    make(map[string]string),
		}, nil
	}

	op, err := newSqlDB(cfg)
	if err != nil {
		return nil, err
	}

	return &Reader{
		api:          op,
		prefix:       cfg.prefix,
		ownsAPI:      true,
		pollInterval: cfg.pollInterval,
		values:       make(map[string]string),
		checksums:    make(map[string]string),
	}, nil
}

func (r *Reader) resolveKey(key string) string {
	return r.prefix + key
}

func (r *Reader) Load(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	resolved := r.resolveKey(key)
	val, checksum, err := r.api.GetScript(ctx, resolved)
	if err != nil {
		return "", err
	}

	r.mu.Lock()
	r.values[key] = val
	r.checksums[key] = checksum
	r.mu.Unlock()

	return val, nil
}

func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}
	r.closed = true

	if r.ownsAPI && r.api != nil {
		return r.api.Close()
	}
	return nil
}

func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
