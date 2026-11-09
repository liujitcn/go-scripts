package consul

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liujitcn/go-scripts/source"
)

type fakeConsul struct {
	mu      sync.Mutex
	data    map[string]*api.KVPair
	getHits atomic.Int64
	idx     uint64
}

func newFakeConsul() *fakeConsul {
	return &fakeConsul{
		data: make(map[string]*api.KVPair),
	}
}

func (f *fakeConsul) nextIndex() uint64 {
	f.idx++
	return f.idx
}

func (f *fakeConsul) set(key, val string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	old, ok := f.data[key]
	newIdx := f.idx + 1
	f.idx = newIdx
	if ok {
		old.Value = []byte(val)
		old.ModifyIndex = newIdx
		f.data[key] = old
	} else {
		f.data[key] = &api.KVPair{
			Key:         key,
			Value:       []byte(val),
			CreateIndex: newIdx,
			ModifyIndex: newIdx,
		}
	}
}

func (f *fakeConsul) del(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, key)
}

// Get 模拟读取指定键值并记录调用次数。
func (f *fakeConsul) Get(key string, _ *api.QueryOptions) (*api.KVPair, *api.QueryMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getHits.Add(1)
	pair, ok := f.data[key]
	if !ok {
		return nil, &api.QueryMeta{}, nil
	}
	return pair, &api.QueryMeta{}, nil
}

func (f *fakeConsul) List(prefix string, _ *api.QueryOptions) (api.KVPairs, *api.QueryMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out api.KVPairs
	for k, v := range f.data {
		if hasPrefix(k, prefix) {
			out = append(out, v)
		}
	}
	return out, &api.QueryMeta{}, nil
}

func hasPrefix(s, prefix string) bool {
	if len(prefix) == 0 {
		return true
	}
	if len(s) < len(prefix) {
		return false
	}
	return s[:len(prefix)] == prefix
}

func newTestReader(t *testing.T, f *fakeConsul, opts ...Option) *Reader {
	t.Helper()
	ctx := context.Background()
	all := append([]Option{withClient(f, nil)}, opts...)
	src, err := New(ctx, all...)
	require.NoError(t, err)
	return src
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestReader_ImplementsInterface(t *testing.T) {
	var _ source.Reader = (*Reader)(nil)
	var _ source.ReadWatcher = (*Reader)(nil)
}

// TestWithPrefix_Normalized 验证脚本源前缀按既定规则规范化。
func TestWithPrefix_Normalized(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"/", ""},
		{"scripts", "scripts"},
		{"/scripts", "scripts"},
		{"scripts/", "scripts/"},
		{"/scripts/", "scripts/"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			cfg := &configOptions{}
			WithPrefix(tc.in)(cfg)
			assert.Equal(t, tc.want, cfg.prefix)
		})
	}
}

// TestLoad_HappyPath 验证脚本读取成功并记录预期的远端调用次数。
func TestLoad_HappyPath(t *testing.T) {
	f := newFakeConsul()
	f.set("hello.lua", "print('hi')")

	src := newTestReader(t, f)
	defer src.Close()

	code, err := src.Load(context.Background(), "hello.lua")
	require.NoError(t, err)
	assert.Equal(t, "print('hi')", code)
	assert.Equal(t, int64(1), f.getHits.Load())
}

func TestLoad_NotFound_WrapsSentinel(t *testing.T) {
	f := newFakeConsul()
	src := newTestReader(t, f)
	defer src.Close()

	_, err := src.Load(context.Background(), "missing.lua")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound), "want ErrNotFound, got %v", err)
	assert.True(t, IsNotFound(err))
}

func TestLoad_WithPrefix(t *testing.T) {
	f := newFakeConsul()
	f.set("scripts/lua/main.lua", "return 1")

	src := newTestReader(t, f, WithPrefix("scripts/lua/"))
	defer src.Close()

	code, err := src.Load(context.Background(), "main.lua")
	require.NoError(t, err)
	assert.Equal(t, "return 1", code)
}

func TestLoad_ContextCanceled(t *testing.T) {
	f := newFakeConsul()
	src := newTestReader(t, f)
	defer src.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := src.Load(ctx, "any")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestClose_NoError(t *testing.T) {
	f := newFakeConsul()
	src := newTestReader(t, f)
	assert.NoError(t, src.Close())
}

// TestLoad_Concurrent 验证脚本源在并发读取时返回一致结果。
func TestLoad_Concurrent(t *testing.T) {
	f := newFakeConsul()
	f.set("shared.lua", "-- shared body\n")

	src := newTestReader(t, f)
	defer src.Close()

	const goroutines = 30
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			code, err := src.Load(context.Background(), "shared.lua")
			assert.NoError(t, err)
			assert.Equal(t, "-- shared body\n", code)
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(goroutines), f.getHits.Load())
}

func TestWatch_KeyModified(t *testing.T) {
	f := newFakeConsul()
	f.set("script.lua", "v1")

	src := newTestReader(t, f)
	defer src.Close()

	_, err := src.Load(context.Background(), "script.lua")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	ch, err := src.Watch(ctx, "script.lua")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)
	f.set("script.lua", "v2")

	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("timeout waiting for watch signal")
	}
}

func TestWatch_KeyDeleted(t *testing.T) {
	f := newFakeConsul()
	f.set("script.lua", "v1")

	src := newTestReader(t, f)
	defer src.Close()

	_, err := src.Load(context.Background(), "script.lua")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	ch, err := src.Watch(ctx, "script.lua")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)
	f.del("script.lua")

	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("timeout waiting for watch signal")
	}
}

func TestWatch_ContextCancelled(t *testing.T) {
	f := newFakeConsul()
	f.set("script.lua", "v1")

	src := newTestReader(t, f)
	defer src.Close()

	_, err := src.Load(context.Background(), "script.lua")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := src.Watch(ctx, "script.lua")
	require.NoError(t, err)

	cancel()

	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for channel close")
	}
}

func TestWatch_NotLoaded(t *testing.T) {
	f := newFakeConsul()
	src := newTestReader(t, f)
	defer src.Close()

	_, err := src.Watch(context.Background(), "never-loaded.lua")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has not been loaded")
}
