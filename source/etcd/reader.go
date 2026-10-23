package etcd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/liujitcn/go-scripts/source"
)

type Reader struct {
	client clientAPI
	closer ioCloser

	prefix string

	mu     sync.RWMutex
	values map[string]string // key -> last loaded value
}

// 编译期断言：实现满足目标接口。
var _ source.Reader = (*Reader)(nil)

// 编译期断言：实现满足目标接口。
var _ source.ReadWatcher = (*Reader)(nil)

func withClient(c clientAPI, closer ioCloser) Option {
	return func(o *configOptions) {
		o.client = c
		o.closer = closer
	}
}

func New(_ context.Context, opts ...Option) (*Reader, error) {
	cfg := &configOptions{
		endpoints: []string{"localhost:2379"},
		timeout:   5 * time.Second,
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

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.endpoints,
		DialTimeout: cfg.timeout,
		Username:    cfg.username,
		Password:    cfg.password,
	})
	if err != nil {
		return nil, fmt.Errorf("etcd source: create client: %w", err)
	}

	return &Reader{
		client: cli,
		closer: cli,
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

	etcdKey := r.resolveKey(key)
	resp, err := r.client.Get(ctx, etcdKey)
	if err != nil {
		return "", fmt.Errorf("etcd source: get %q: %w", etcdKey, err)
	}

	if len(resp.Kvs) == 0 {
		return "", fmt.Errorf("%w: %s", ErrNotFound, etcdKey)
	}

	val := string(resp.Kvs[0].Value)

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
