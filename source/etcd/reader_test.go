package etcd

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/liujitcn/go-scripts/source"
)

type fakeEtcd struct {
	mu       sync.Mutex
	data     map[string]string
	getHits  int64
	watchers map[string][]chan clientv3.WatchResponse
}

func newFakeEtcd() *fakeEtcd {
	return &fakeEtcd{
		data:     make(map[string]string),
		watchers: make(map[string][]chan clientv3.WatchResponse),
	}
}

func (f *fakeEtcd) set(key, val string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[key] = val
	for _, ch := range f.watchers[key] {
		evt := &clientv3.Event{
			Type: clientv3.EventTypePut,
			Kv:   &mvccpb.KeyValue{Key: []byte(key), Value: []byte(val)},
		}
		select {
		case ch <- clientv3.WatchResponse{Events: []*clientv3.Event{evt}}:
		default:
		}
	}
}

func (f *fakeEtcd) del(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, key)
	for _, ch := range f.watchers[key] {
		evt := &clientv3.Event{
			Type: clientv3.EventTypeDelete,
			Kv:   &mvccpb.KeyValue{Key: []byte(key)},
		}
		select {
		case ch <- clientv3.WatchResponse{Events: []*clientv3.Event{evt}}:
		default:
		}
	}
}

func (f *fakeEtcd) Get(_ context.Context, key string, _ ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	atomic.AddInt64(&f.getHits, 1)
	val, ok := f.data[key]
	if !ok {
		return &clientv3.GetResponse{Kvs: nil}, nil
	}
	return &clientv3.GetResponse{
		Kvs: []*mvccpb.KeyValue{
			{Key: []byte(key), Value: []byte(val)},
		},
	}, nil
}

func (f *fakeEtcd) Watch(ctx context.Context, key string, _ ...clientv3.OpOption) clientv3.WatchChan {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan clientv3.WatchResponse, 16)
	f.watchers[key] = append(f.watchers[key], ch)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch
}

func newTestReader(t *testing.T, f *fakeEtcd, opts ...Option) *Reader {
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
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			cfg := &configOptions{}
			WithPrefix(tc.in)(cfg)
			assert.Equal(t, tc.want, cfg.prefix)
		})
	}
}

func TestLoad_HappyPath(t *testing.T) {
	f := newFakeEtcd()
	f.set("hello.lua", "print('hi')")

	src := newTestReader(t, f)
	defer src.Close()

	code, err := src.Load(context.Background(), "hello.lua")
	require.NoError(t, err)
	assert.Equal(t, "print('hi')", code)
	assert.Equal(t, int64(1), atomic.LoadInt64(&f.getHits))
}

func TestLoad_NotFound_WrapsSentinel(t *testing.T) {
	f := newFakeEtcd()
	src := newTestReader(t, f)
	defer src.Close()

	_, err := src.Load(context.Background(), "missing.lua")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound), "want ErrNotFound, got %v", err)
	assert.True(t, IsNotFound(err))
}

func TestLoad_WithPrefix(t *testing.T) {
	f := newFakeEtcd()
	f.set("scripts/lua/main.lua", "return 1")

	src := newTestReader(t, f, WithPrefix("scripts/lua/"))
	defer src.Close()

	code, err := src.Load(context.Background(), "main.lua")
	require.NoError(t, err)
	assert.Equal(t, "return 1", code)
}

func TestLoad_ContextCanceled(t *testing.T) {
	f := newFakeEtcd()
	src := newTestReader(t, f)
	defer src.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := src.Load(ctx, "any")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestClose_NoError(t *testing.T) {
	f := newFakeEtcd()
	src := newTestReader(t, f)
	assert.NoError(t, src.Close())
}

func TestLoad_Concurrent(t *testing.T) {
	f := newFakeEtcd()
	f.set("shared.lua", "-- shared body\n")

	src := newTestReader(t, f)
	defer src.Close()

	const goroutines = 30
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			code, err := src.Load(context.Background(), "shared.lua")
			assert.NoError(t, err)
			assert.Equal(t, "-- shared body\n", code)
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(goroutines), atomic.LoadInt64(&f.getHits))
}

func TestWatch_KeyModified(t *testing.T) {
	f := newFakeEtcd()
	f.set("script.lua", "v1")

	src := newTestReader(t, f)
	defer src.Close()

	_, err := src.Load(context.Background(), "script.lua")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := src.Watch(ctx, "script.lua")
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	f.set("script.lua", "v2")

	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("timeout waiting for watch signal")
	}
}

func TestWatch_KeyDeleted(t *testing.T) {
	f := newFakeEtcd()
	f.set("script.lua", "v1")

	src := newTestReader(t, f)
	defer src.Close()

	_, err := src.Load(context.Background(), "script.lua")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := src.Watch(ctx, "script.lua")
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	f.del("script.lua")

	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("timeout waiting for watch signal")
	}
}

func TestWatch_ContextCancelled(t *testing.T) {
	f := newFakeEtcd()
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
	f := newFakeEtcd()
	src := newTestReader(t, f)
	defer src.Close()

	_, err := src.Watch(context.Background(), "never-loaded.lua")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has not been loaded")
}

func TestWatch_Concurrent(t *testing.T) {
	f := newFakeEtcd()
	f.set("shared.lua", "v1")

	src := newTestReader(t, f)
	defer src.Close()

	_, err := src.Load(context.Background(), "shared.lua")
	require.NoError(t, err)

	const watchers = 5
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	chs := make([]<-chan struct{}, watchers)
	for i := 0; i < watchers; i++ {
		ch, err := src.Watch(ctx, "shared.lua")
		require.NoError(t, err)
		chs[i] = ch
	}

	time.Sleep(50 * time.Millisecond)
	f.set("shared.lua", "v2")

	for i := 0; i < watchers; i++ {
		select {
		case <-chs[i]:
		case <-ctx.Done():
			t.Fatalf("watcher %d: timeout waiting for signal", i)
		}
	}
}
