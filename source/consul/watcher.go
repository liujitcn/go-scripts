package consul

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/consul/api"
)

//

func (r *Reader) Watch(ctx context.Context, key string) (<-chan struct{}, error) {

	r.mu.RLock()
	lastIdx, ok := r.indexs[key]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("consul source: key %q has not been loaded yet; call Load first", key)
	}

	consulKey := r.resolveKey(key)
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
				q := &api.QueryOptions{}
				q = q.WithContext(ctx)

				pair, _, err := r.client.Get(consulKey, q)
				if err != nil {

					continue
				}
				if pair == nil {

					if lastIdx != 0 {
						lastIdx = 0
						select {
						case ch <- struct{}{}:
						default:
						}
					}
					continue
				}

				if pair.ModifyIndex != lastIdx {
					lastIdx = pair.ModifyIndex

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
