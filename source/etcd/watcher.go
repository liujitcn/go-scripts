package etcd

import (
	"context"
)

//

func (r *Reader) Watch(ctx context.Context, key string) (<-chan struct{}, error) {

	r.mu.RLock()
	_, ok := r.values[key]
	r.mu.RUnlock()
	if !ok {
		return nil, errNotLoaded(key)
	}

	etcdKey := r.resolveKey(key)

	watchCh := r.client.Watch(ctx, etcdKey)

	ch := make(chan struct{}, 1)

	go func() {
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case resp, ok := <-watchCh:
				if !ok {

					return
				}
				if resp.Canceled {
					return
				}

				if len(resp.Events) > 0 {
					select {
					case ch <- struct{}{}:
					default:
					}
				}
			}
		}
	}()

	return ch, nil
}

func errNotLoaded(key string) error {
	return notLoadedError{key: key}
}

type notLoadedError struct{ key string }

func (e notLoadedError) Error() string {
	return "etcd source: key \"" + e.key + "\" has not been loaded yet; call Load first"
}
