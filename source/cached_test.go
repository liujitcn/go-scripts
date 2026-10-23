package source

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCachedSource_ImplementsInterface(t *testing.T) {
	var _ Reader = (*CachedSource)(nil)
	var _ ReadWatcher = (*CachedSource)(nil)
}

func TestNewCachedSource_NilRemote(t *testing.T) {
	_, err := NewCachedSource(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote reader is nil")
}

func TestCached_Load_CacheMiss(t *testing.T) {
	remote := &fakeSource{code: "from-remote"}
	c, err := NewCachedSource(remote)
	require.NoError(t, err)
	defer c.Close()

	code, err := c.Load(context.Background(), "k")
	require.NoError(t, err)
	assert.Equal(t, "from-remote", code)
	assert.Equal(t, 1, remote.loadCalls)
}

func TestCached_Load_CacheHit(t *testing.T) {
	remote := &fakeSource{code: "from-remote"}
	c, err := NewCachedSource(remote)
	require.NoError(t, err)
	defer c.Close()

	_, err = c.Load(context.Background(), "k")
	require.NoError(t, err)

	code, err := c.Load(context.Background(), "k")
	require.NoError(t, err)
	assert.Equal(t, "from-remote", code)

	remote.mu.Lock()
	assert.Equal(t, 1, remote.loadCalls) // remote only called once
	remote.mu.Unlock()
}

func TestCached_Load_RemoteError(t *testing.T) {
	customErr := errors.New("network timeout")
	remote := &fakeSource{err: customErr}
	c, err := NewCachedSource(remote)
	require.NoError(t, err)
	defer c.Close()

	_, err = c.Load(context.Background(), "k")
	require.Error(t, err)
	assert.ErrorIs(t, err, customErr)
}

func TestCached_Invalidate(t *testing.T) {
	remote := &fakeSource{code: "v1"}
	c, err := NewCachedSource(remote)
	require.NoError(t, err)
	defer c.Close()

	_, _ = c.Load(context.Background(), "k")

	c.Invalidate("k")

	remote.mu.Lock()
	remote.code = "v2"
	remote.mu.Unlock()

	code, err := c.Load(context.Background(), "k")
	require.NoError(t, err)
	assert.Equal(t, "v2", code)
}

func TestCached_InvalidateAll(t *testing.T) {
	remote := &fakeSource{code: "v1"}
	c, err := NewCachedSource(remote)
	require.NoError(t, err)
	defer c.Close()

	_, _ = c.Load(context.Background(), "k1")
	_, _ = c.Load(context.Background(), "k2")

	c.InvalidateAll()

	remote.mu.Lock()
	initialCalls := remote.loadCalls
	remote.mu.Unlock()

	_, _ = c.Load(context.Background(), "k1")
	_, _ = c.Load(context.Background(), "k2")

	remote.mu.Lock()
	assert.Equal(t, initialCalls+2, remote.loadCalls)
	remote.mu.Unlock()
}

func TestCached_TTL(t *testing.T) {
	remote := &fakeSource{code: "v1"}
	c, err := NewCachedSource(remote, WithTTL(50*time.Millisecond))
	require.NoError(t, err)
	defer c.Close()

	_, _ = c.Load(context.Background(), "k")
	time.Sleep(80 * time.Millisecond) // wait for TTL to expire

	remote.mu.Lock()
	remote.code = "v2"
	remote.mu.Unlock()

	code, err := c.Load(context.Background(), "k")
	require.NoError(t, err)
	assert.Equal(t, "v2", code)
}

func TestCached_TTL_NotExpired(t *testing.T) {
	remote := &fakeSource{code: "v1"}
	c, err := NewCachedSource(remote, WithTTL(5*time.Second))
	require.NoError(t, err)
	defer c.Close()

	_, _ = c.Load(context.Background(), "k")

	_, _ = c.Load(context.Background(), "k")

	remote.mu.Lock()
	assert.Equal(t, 1, remote.loadCalls)
	remote.mu.Unlock()
}

func TestCached_WatchInvalidation(t *testing.T) {
	mem := NewMemSource()
	mem.Set("k", "v1")

	c, err := NewCachedSource(mem)
	require.NoError(t, err)
	defer c.Close()

	code, err := c.Load(context.Background(), "k")
	require.NoError(t, err)
	assert.Equal(t, "v1", code)

	time.Sleep(50 * time.Millisecond)
	mem.Set("k", "v2")

	time.Sleep(100 * time.Millisecond)

	code, err = c.Load(context.Background(), "k")
	require.NoError(t, err)
	assert.Equal(t, "v2", code)
}

func TestCached_Watch_Delegates(t *testing.T) {
	mem := NewMemSource()
	mem.Set("k", "v1")

	c, err := NewCachedSource(mem)
	require.NoError(t, err)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := c.Watch(ctx, "k")
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	mem.Set("k", "v2")

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for watch signal")
	}
}

func TestCached_Watch_NotSupported(t *testing.T) {
	remote := &fakeSource{code: "x"}
	c, err := NewCachedSource(remote)
	require.NoError(t, err)
	defer c.Close()

	_, err = c.Watch(context.Background(), "k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not implement Watcher")
}

func TestCached_Close_StopsWatchers(t *testing.T) {
	mem := NewMemSource()
	mem.Set("k", "v1")

	c, err := NewCachedSource(mem)
	require.NoError(t, err)

	_, _ = c.Load(context.Background(), "k")

	err = c.Close()
	require.NoError(t, err)

	c.mu.Lock()
	assert.Equal(t, 0, len(c.watchers))
	c.mu.Unlock()
}

func TestCached_Concurrent(t *testing.T) {
	remote := &fakeSource{code: "shared"}
	c, err := NewCachedSource(remote)
	require.NoError(t, err)
	defer c.Close()

	const goroutines = 30
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			code, err := c.Load(context.Background(), "k")
			assert.NoError(t, err)
			assert.Equal(t, "shared", code)
		}()
	}
	wg.Wait()

	remote.mu.Lock()
	assert.LessOrEqual(t, remote.loadCalls, goroutines)
	remote.mu.Unlock()
}

func TestCached_Load_MultipleKeys(t *testing.T) {
	var counter int64
	remote := &countingSource{counter: &counter, code: "data"}

	c, err := NewCachedSource(remote)
	require.NoError(t, err)
	defer c.Close()

	_, _ = c.Load(context.Background(), "k1")
	_, _ = c.Load(context.Background(), "k2")
	_, _ = c.Load(context.Background(), "k3")

	assert.Equal(t, int64(3), atomic.LoadInt64(&counter))

	_, _ = c.Load(context.Background(), "k1")
	_, _ = c.Load(context.Background(), "k2")
	_, _ = c.Load(context.Background(), "k3")

	assert.Equal(t, int64(3), atomic.LoadInt64(&counter))
}

type countingSource struct {
	code    string
	counter *int64
}

func (s *countingSource) Load(_ context.Context, _ string) (string, error) {
	atomic.AddInt64(s.counter, 1)
	return s.code, nil
}

func (s *countingSource) Close() error { return nil }
