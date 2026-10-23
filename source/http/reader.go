package http

import (
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/liujitcn/go-scripts/source"
)

type Reader struct {
	client  *http.Client
	headers map[string]string

	baseURL string
	prefix  string

	mu        sync.RWMutex
	checksums map[string]uint32 // key -> CRC32 of last loaded body
}

// 编译期断言：实现满足目标接口。
var _ source.Reader = (*Reader)(nil)

// 编译期断言：实现满足目标接口。
var _ source.ReadWatcher = (*Reader)(nil)

func New(_ context.Context, opts ...Option) (*Reader, error) {
	cfg := &configOptions{
		timeout: 30 * time.Second,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.baseURL == "" {
		return nil, fmt.Errorf("http source: base URL must be set via WithBaseURL")
	}

	client := cfg.httpClient
	if client == nil {
		client = &http.Client{Timeout: cfg.timeout}
	}

	return &Reader{
		client:    client,
		headers:   cfg.headers,
		baseURL:   cfg.baseURL,
		prefix:    cfg.prefix,
		checksums: make(map[string]uint32),
	}, nil
}

func (r *Reader) resolveKey(key string) string {
	return r.baseURL + r.prefix + key
}

func (r *Reader) Load(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	url := r.resolveKey(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("http source: create request for %q: %w", url, err)
	}

	for k, v := range r.headers {
		req.Header.Set(k, v)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http source: get %q: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("%w: %s", ErrNotFound, url)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http source: get %q: HTTP %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("http source: read body %q: %w", url, err)
	}

	r.mu.Lock()
	r.checksums[key] = crc32.ChecksumIEEE(body)
	r.mu.Unlock()

	return string(body), nil
}

func (r *Reader) Close() error {
	r.client.CloseIdleConnections()
	return nil
}

func IsNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), ErrNotFound.Error())
}
