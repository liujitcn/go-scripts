package redis

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type Option func(*configOptions)

type configOptions struct {
	addr     string
	password string
	username string
	db       int
	prefix   string
	client   redisCmdable // for tests; when non-nil a real client is not created
	closer   io.Closer    // optional closer for the test client; may be nil
}

type redisCmdable interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	Ping(ctx context.Context) *redis.StatusCmd
}

func WithAddr(addr string) Option {
	return func(o *configOptions) { o.addr = addr }
}

func WithPassword(password string) Option {
	return func(o *configOptions) { o.password = password }
}

func WithUsername(username string) Option {
	return func(o *configOptions) { o.username = username }
}

func WithDB(db int) Option {
	return func(o *configOptions) { o.db = db }
}

//

func WithPrefix(prefix string) Option {
	return func(o *configOptions) {
		prefix = strings.TrimPrefix(prefix, "/")
		o.prefix = prefix
	}
}
