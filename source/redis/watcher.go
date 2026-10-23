package redis

import (
	"context"
	"fmt"
	"time"
)

//

func (r *Reader) Watch(ctx context.Context, key string) (<-chan struct{}, error) {

	r.mu.RLock()
	_, ok := r.values[key]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("redis source: key %q has not been loaded yet; call Load first", key)
	}

	ch := make(chan struct{})

	go func() {
		defer close(ch)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if r.hasValueChanged(ctx, key) {

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
