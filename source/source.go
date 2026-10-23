package source

import (
	"context"
)

type Reader interface {
	Load(ctx context.Context, key string) (code string, err error)

	Close() error
}

// 支持热更新的实现会在底层脚本源发生变化时通过通道发送通知。
type Watcher interface {
	Watch(ctx context.Context, key string) (<-chan struct{}, error)
}

type ReadWatcher interface {
	Reader
	Watcher
}
