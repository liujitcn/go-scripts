package git

import (
	"context"
	"fmt"
	"time"
)

//

//

func (r *Reader) Watch(ctx context.Context, key string) (<-chan struct{}, error) {

	r.mu.RLock()
	baseline, ok := r.hashes[key]
	r.mu.RUnlock()
	if !ok {
		return nil, errNotLoaded(key)
	}

	interval := r.pullInterval
	if interval <= 0 {
		interval = defaultPullInterval
	}

	ch := make(chan struct{}, 1)

	go func() {
		defer close(ch)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:

				if err := r.op.Pull(ctx, r.path); err != nil {

					continue
				}

				currentHash, err := r.op.HeadHash(r.path)
				if err != nil {
					continue
				}

				if currentHash != baseline {
					baseline = currentHash

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
	return fmt.Sprintf("git source: key %q has not been loaded yet; call Load first", e.key)
}
