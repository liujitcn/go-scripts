package etcd

import (
	"context"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

type Option func(*configOptions)

type configOptions struct {
	endpoints []string
	username  string
	password  string
	prefix    string
	timeout   time.Duration
	client    clientAPI // for tests; when non-nil a real client is not created
	closer    ioCloser  // optional closer for the test client; may be nil
}

type ioCloser interface {
	Close() error
}

type clientAPI interface {
	Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error)
	Watch(ctx context.Context, key string, opts ...clientv3.OpOption) clientv3.WatchChan
}

func WithEndpoints(endpoints ...string) Option {
	return func(o *configOptions) { o.endpoints = endpoints }
}

func WithUsername(username string) Option {
	return func(o *configOptions) { o.username = username }
}

func WithPassword(password string) Option {
	return func(o *configOptions) { o.password = password }
}

//

func WithPrefix(prefix string) Option {
	return func(o *configOptions) {
		prefix = strings.TrimPrefix(prefix, "/")
		o.prefix = prefix
	}
}

func WithTimeout(timeout time.Duration) Option {
	return func(o *configOptions) { o.timeout = timeout }
}
