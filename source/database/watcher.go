package database

import (
	"context"
	"fmt"
	"time"
)

//

func (r *Reader) Watch(ctx context.Context, key string) (<-chan struct{}, error) {

	r.mu.RLock()
	baseline, ok := r.checksums[key]
	r.mu.RUnlock()
	if !ok {
		return nil, errNotLoaded(key)
	}

	interval := r.pollInterval
	if interval <= 0 {
		interval = defaultPollInterval
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

				resolved := r.resolveKey(key)
				_, currentChecksum, err := r.api.GetScript(ctx, resolved)
				if err != nil {

					if IsNotFound(err) && baseline != "" {
						baseline = ""
						select {
						case ch <- struct{}{}:
						default:
						}
					}
					continue
				}

				if currentChecksum != baseline {
					baseline = currentChecksum

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
	return fmt.Sprintf("database source: key %q has not been loaded yet; call Load first", e.key)
}
