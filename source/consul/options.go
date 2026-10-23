package consul

import (
	"strings"
	"time"

	"github.com/hashicorp/consul/api"
)

type Option func(*configOptions)

type ioCloser interface {
	Close() error
}

type configOptions struct {
	address string
	token   string
	prefix  string
	timeout time.Duration
	client  consulAPI // for tests; when non-nil a real client is not created
	closer  ioCloser  // optional closer for the test client; may be nil
}

type consulAPI interface {
	Get(key string, q *api.QueryOptions) (*api.KVPair, *api.QueryMeta, error)
	List(prefix string, q *api.QueryOptions) (api.KVPairs, *api.QueryMeta, error)
}

type kvClient struct{ kv *api.KV }

func (c kvClient) Get(key string, q *api.QueryOptions) (*api.KVPair, *api.QueryMeta, error) {
	return c.kv.Get(key, q)
}

func (c kvClient) List(prefix string, q *api.QueryOptions) (api.KVPairs, *api.QueryMeta, error) {
	return c.kv.List(prefix, q)
}

func WithAddress(address string) Option {
	return func(o *configOptions) { o.address = address }
}

func WithToken(token string) Option {
	return func(o *configOptions) { o.token = token }
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
