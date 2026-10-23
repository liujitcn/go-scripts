package http

import (
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"time"
)

//

func (r *Reader) Watch(ctx context.Context, key string) (<-chan struct{}, error) {

	r.mu.RLock()
	_, ok := r.checksums[key]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("http source: key %q has not been loaded yet; call Load first", key)
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
				if r.hasContentChanged(ctx, key) {
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

func (r *Reader) hasContentChanged(ctx context.Context, key string) bool {
	url := r.resolveKey(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}

	currentSum := crc32.ChecksumIEEE(body)

	r.mu.RLock()
	stored := r.checksums[key]
	r.mu.RUnlock()

	return currentSum != stored
}
