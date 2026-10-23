package source

import (
	"context"
	"errors"
	"fmt"
)

// MultiStrategy 定义多个脚本源的读取策略。
type MultiStrategy int

const (
	// MultiStrategyFallback 按顺序读取，返回第一个成功结果。
	MultiStrategyFallback MultiStrategy = iota

	// MultiStrategyFirstOK 并发读取，返回第一个成功结果并取消其他请求。
	MultiStrategyFirstOK
)

// MultiSource 按配置策略组合多个 Reader，并将读取和关闭操作统一转发。
type MultiSource struct {
	sources  []Reader
	strategy MultiStrategy
}

// NewMultiSource 创建组合脚本源；sources 不能为空且不能包含 nil。
func NewMultiSource(strategy MultiStrategy, sources ...Reader) (*MultiSource, error) {
	if len(sources) == 0 {
		return nil, errors.New("multi source: at least one source is required")
	}
	for i, s := range sources {
		if s == nil {
			return nil, fmt.Errorf("multi source: source[%d] is nil", i)
		}
	}
	return &MultiSource{sources: sources, strategy: strategy}, nil
}

// NewFallbackSource 创建按顺序回退的组合脚本源。
func NewFallbackSource(sources ...Reader) (*MultiSource, error) {
	return NewMultiSource(MultiStrategyFallback, sources...)
}

// NewFirstOKSource 创建并发读取并返回首个成功结果的组合脚本源。
func NewFirstOKSource(sources ...Reader) (*MultiSource, error) {
	return NewMultiSource(MultiStrategyFirstOK, sources...)
}

func (ms *MultiSource) Load(ctx context.Context, key string) (string, error) {
	switch ms.strategy {
	case MultiStrategyFirstOK:
		return ms.loadFirstOK(ctx, key)
	default: // MultiStrategyFallback
		return ms.loadFallback(ctx, key)
	}
}

func (ms *MultiSource) loadFallback(ctx context.Context, key string) (string, error) {
	var errs []error
	for _, s := range ms.sources {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		code, err := s.Load(ctx, key)
		if err == nil {
			return code, nil
		}
		errs = append(errs, err)
	}
	return "", fmt.Errorf("multi source(fallback): all sources failed for %q: %w", key, errors.Join(errs...))
}

func (ms *MultiSource) loadFirstOK(ctx context.Context, key string) (string, error) {
	type result struct {
		code string
		err  error
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ch := make(chan result, len(ms.sources))
	for _, s := range ms.sources {
		s := s
		go func() {
			code, err := s.Load(ctx, key)
			ch <- result{code, err}
		}()
	}

	var errs []error
	for i := 0; i < len(ms.sources); i++ {
		r := <-ch
		if r.err == nil {
			cancel() // tell other goroutines to bail out ASAP
			return r.code, nil
		}
		errs = append(errs, r.err)
	}
	return "", fmt.Errorf("multi source(first-ok): all sources failed for %q: %w", key, errors.Join(errs...))
}

// Close 关闭所有子脚本源，并聚合关闭错误。
func (ms *MultiSource) Close() error {
	var errs []error
	for _, s := range ms.sources {
		if err := s.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("multi source: close errors: %w", errors.Join(errs...))
}

// Watch 返回第一个支持 Watch 的子脚本源的通知通道。

func (ms *MultiSource) Watch(ctx context.Context, key string) (<-chan struct{}, error) {
	var errs []error
	for i, s := range ms.sources {
		if w, ok := s.(Watcher); ok {
			ch, err := w.Watch(ctx, key)
			if err == nil {
				return ch, nil
			}
			errs = append(errs, fmt.Errorf("source[%d]: %w", i, err))
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("multi source: no watcher available: %w", errors.Join(errs...))
	}
	return nil, errors.New("multi source: none of the sub-sources implement Watcher")
}

// 编译期断言：实现满足目标接口。
var _ ReadWatcher = (*MultiSource)(nil)
