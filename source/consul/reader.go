package consul

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/liujitcn/go-scripts/source"
)

type Reader struct {
	client consulAPI
	closer ioCloser

	prefix string

	mu     sync.RWMutex
	values map[string]string // key -> last loaded value
	indexs map[string]uint64 // key -> last ModifyIndex
}

// 编译期断言：实现满足目标接口。
var _ source.Reader = (*Reader)(nil)

// 编译期断言：实现满足目标接口。
var _ source.ReadWatcher = (*Reader)(nil)

func withClient(c consulAPI, closer ioCloser) Option {
	return func(o *configOptions) {
		o.client = c
		o.closer = closer
	}
}

func New(_ context.Context, opts ...Option) (*Reader, error) {
	cfg := &configOptions{
		address: "127.0.0.1:8500",
		timeout: 5 * time.Second,
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
			indexs: make(map[string]uint64),
		}, nil
	}

	consulCfg := api.DefaultConfig()
	if cfg.address != "" {
		consulCfg.Address = cfg.address
	}
	if cfg.token != "" {
		consulCfg.Token = cfg.token
	}
	if cfg.timeout > 0 {
		consulCfg.WaitTime = cfg.timeout
	}

	cli, err := api.NewClient(consulCfg)
	if err != nil {
		return nil, fmt.Errorf("consul source: create client: %w", err)
	}

	return &Reader{
		client: kvClient{kv: cli.KV()},
		closer: nil, // api.Client has no explicit Close
		prefix: cfg.prefix,
		values: make(map[string]string),
		indexs: make(map[string]uint64),
	}, nil
}

func (r *Reader) resolveKey(key string) string {
	return r.prefix + key
}

func (r *Reader) Load(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	consulKey := r.resolveKey(key)
	q := &api.QueryOptions{}
	q = q.WithContext(ctx)

	pair, _, err := r.client.Get(consulKey, q)
	if err != nil {
		return "", fmt.Errorf("consul source: get %q: %w", consulKey, err)
	}

	if pair == nil {
		return "", fmt.Errorf("%w: %s", ErrNotFound, consulKey)
	}

	val := string(pair.Value)

	r.mu.Lock()
	r.values[key] = val
	r.indexs[key] = pair.ModifyIndex
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
