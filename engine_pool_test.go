package script_engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liujitcn/go-scripts/source"
)

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

const poolTestType Type = "mock-pool"

func withPoolFactory(t *testing.T, initErr, closeErr error) (*int64, func()) {
	t.Helper()
	Unregister(poolTestType)
	var created int64
	require.NoError(t, Register(poolTestType, func() (Engine, error) {
		m := newMockEngine(poolTestType)
		m.initErr = initErr
		m.closeErr = closeErr
		atomic.AddInt64(&created, 1)
		return m, nil
	}))
	return &created, func() { Unregister(poolTestType) }
}

func TestNewEnginePool_InvalidSize(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	_, err := NewEnginePool(0, poolTestType)
	require.Error(t, err)

	_, err = NewEnginePool(-1, poolTestType)
	require.Error(t, err)
}

func TestNewEnginePool_FactoryError(t *testing.T) {
	Unregister(poolTestType)
	require.NoError(t, Register(poolTestType, func() (Engine, error) {
		return nil, errors.New("factory boom")
	}))
	defer Unregister(poolTestType)

	_, err := NewEnginePool(3, poolTestType)
	require.Error(t, err)
}

func TestNewEnginePool_InitError(t *testing.T) {
	Unregister(poolTestType)
	var createdMu sync.Mutex
	var createdEngines []*mockEngine
	require.NoError(t, Register(poolTestType, func() (Engine, error) {
		m := newMockEngine(poolTestType)
		m.initErr = errors.New("init boom")
		createdMu.Lock()
		createdEngines = append(createdEngines, m)
		createdMu.Unlock()
		return m, nil
	}))
	defer Unregister(poolTestType)

	_, err := NewEnginePool(2, poolTestType)
	require.Error(t, err)

	createdMu.Lock()
	defer createdMu.Unlock()
	require.Len(t, createdEngines, 1)
	assert.Equal(t, 1, createdEngines[0].closeCount, "failed engine must be Closed")
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestEnginePool_AcquireRelease(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewEnginePool(2, poolTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	a, err := pool.Acquire()
	require.NoError(t, err)
	assert.NotNil(t, a)

	pool.Release(a)
}

func TestEnginePool_Acquire_BlocksUntilReleased(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewEnginePool(1, poolTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	a, err := pool.Acquire()
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		time.Sleep(10 * time.Millisecond)
		pool.Release(a)
		close(done)
	}()

	b, err := pool.Acquire()
	require.NoError(t, err)
	assert.Same(t, a, b)
	<-done
}

func TestEnginePool_Acquire_AfterClose(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewEnginePool(1, poolTestType)
	require.NoError(t, err)

	require.NoError(t, pool.Close())
	_, err = pool.Acquire()
	require.Error(t, err)
}

func TestEnginePool_Release_NilEngine(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewEnginePool(1, poolTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	pool.Release(nil)
}

func TestEnginePool_Release_AfterClose(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewEnginePool(1, poolTestType)
	require.NoError(t, err)

	eng, err := pool.Acquire()
	require.NoError(t, err)
	require.NoError(t, pool.Close())

	pool.Release(eng)
	mock := eng.(*mockEngine)
	assert.Equal(t, 1, mock.closeCount)
}

func TestEnginePool_Close_Twice(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewEnginePool(1, poolTestType)
	require.NoError(t, err)

	require.NoError(t, pool.Close())
	assert.NoError(t, pool.Close())
}

func TestEnginePool_IsClosed(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewEnginePool(1, poolTestType)
	require.NoError(t, err)
	assert.False(t, pool.IsClosed())

	require.NoError(t, pool.Close())
	assert.True(t, pool.IsClosed())
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestEnginePool_InitAll(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewEnginePool(3, poolTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	for i := 0; i < 3; i++ {
		eng, acqErr := pool.Acquire()
		require.NoError(t, acqErr)
		mock := eng.(*mockEngine)
		mock.initialized = false
		pool.Release(eng)
	}

	require.NoError(t, pool.InitAll(context.Background()))
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestEnginePool_SetSource_GetSource(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewEnginePool(1, poolTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	want := source.NewMemSource()
	pool.SetSource(want)
	got := pool.GetSource()
	assert.Same(t, want, got)
}

func TestEnginePool_Load(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewEnginePool(1, poolTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	src := source.NewMemSource()
	src.Set("k", "code")
	pool.SetSource(src)

	require.NoError(t, pool.Load(context.Background(), "k"))
}

func TestEnginePool_LoadMulti(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewEnginePool(1, poolTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	src := source.NewMemSource()
	src.Set("a", "1")
	src.Set("b", "2")
	pool.SetSource(src)

	require.NoError(t, pool.LoadMulti(context.Background(), []string{"a", "b"}))
}

func TestEnginePool_ExecuteFromKey(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewEnginePool(1, poolTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	src := source.NewMemSource()
	src.Set("k", "code")
	pool.SetSource(src)

	got, err := pool.ExecuteFromKey(context.Background(), "k")
	require.NoError(t, err)
	assert.Equal(t, "exec-result", got)
}

func TestEnginePool_ExecuteFromKeys(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewEnginePool(1, poolTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	src := source.NewMemSource()
	src.Set("a", "1")
	src.Set("b", "2")
	pool.SetSource(src)

	got, err := pool.ExecuteFromKeys(context.Background(), []string{"a", "b"})
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestEnginePool_RegisterGlobal_GetGlobal(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewEnginePool(1, poolTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	require.NoError(t, pool.RegisterGlobal("g", 42))
	got, err := pool.GetGlobal("g")
	require.NoError(t, err)
	assert.Equal(t, 42, got)
}

func TestEnginePool_RegisterModule(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewEnginePool(1, poolTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	require.NoError(t, pool.RegisterModule("m", map[string]any{"k": "v"}))
}

func TestEnginePool_CallFunction(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewEnginePool(1, poolTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	eng, err := pool.Acquire()
	require.NoError(t, err)
	mock := eng.(*mockEngine)
	mock.callResult = "hello"
	pool.Release(eng)

	got, err := pool.CallFunction(context.Background(), "add", 1, 2)
	require.NoError(t, err)
	assert.Equal(t, "hello", got)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestEnginePool_ConcurrentAcquireRelease(t *testing.T) {
	_, cleanup := withPoolFactory(t, nil, nil)
	defer cleanup()

	const size = 4
	pool, err := NewEnginePool(size, poolTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	const goroutines = 50
	const loops = 100
	var wg sync.WaitGroup
	var errCount int64
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < loops; j++ {
				eng, acqErr := pool.Acquire()
				if acqErr != nil {
					atomic.AddInt64(&errCount, 1)
					continue
				}
				_ = eng.GetType()
				pool.Release(eng)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(0), atomic.LoadInt64(&errCount))
}
