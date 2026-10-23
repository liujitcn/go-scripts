package source

import (
	"context"
	"fmt"
)

//

//

//

type TransformFunc func(key string, raw string) (string, error)

//

//

type TransformSource struct {
	inner      Reader
	transforms []TransformFunc
}

// 编译期断言：实现满足目标接口。
var _ Reader = (*TransformSource)(nil)

// 编译期断言：实现满足目标接口。
var _ ReadWatcher = (*TransformSource)(nil)

//

func NewTransformSource(inner Reader, transforms ...TransformFunc) (*TransformSource, error) {
	if inner == nil {
		return nil, fmt.Errorf("transform source: inner reader is nil")
	}
	if len(transforms) == 0 {
		return nil, fmt.Errorf("transform source: at least one transform function is required")
	}
	for i, t := range transforms {
		if t == nil {
			return nil, fmt.Errorf("transform source: transform[%d] is nil", i)
		}
	}
	return &TransformSource{inner: inner, transforms: transforms}, nil
}

func (ts *TransformSource) Load(ctx context.Context, key string) (string, error) {
	raw, err := ts.inner.Load(ctx, key)
	if err != nil {
		return "", fmt.Errorf("transform source: load %q: %w", key, err)
	}

	result := raw
	for i, fn := range ts.transforms {
		result, err = fn(key, result)
		if err != nil {
			return "", fmt.Errorf("transform source: transform[%d] for %q: %w", i, key, err)
		}
	}
	return result, nil
}

func (ts *TransformSource) Close() error {
	return ts.inner.Close()
}

func (ts *TransformSource) Watch(ctx context.Context, key string) (<-chan struct{}, error) {
	w, ok := ts.inner.(Watcher)
	if !ok {
		return nil, fmt.Errorf("transform source: inner reader %T does not implement Watcher", ts.inner)
	}
	return w.Watch(ctx, key)
}

func (ts *TransformSource) Then(fn TransformFunc) (*TransformSource, error) {
	if fn == nil {
		return nil, fmt.Errorf("transform source: then: transform function is nil")
	}
	return &TransformSource{
		inner:      ts.inner,
		transforms: append(append([]TransformFunc{}, ts.transforms...), fn),
	}, nil
}

func IdentityTransform(_ string, raw string) (string, error) {
	return raw, nil
}
