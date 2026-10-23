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

const autogrowTestType Type = "mock-autogrow"

func withAutoGrowFactory(t *testing.T, initErr, closeErr error) (*[]*mockEngine, *sync.Mutex, func()) {
	t.Helper()
	Unregister(autogrowTestType)
	var mu sync.Mutex
	var created []*mockEngine
	require.NoError(t, Register(autogrowTestType, func() (Engine, error) {
		m := newMockEngine(autogrowTestType)
		m.initErr = initErr
		m.closeErr = closeErr
		mu.Lock()
		created = append(created, m)
		mu.Unlock()
		return m, nil
	}))
	return &created, &mu, func() { Unregister(autogrowTestType) }
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestNewAutoGrowEnginePool_InvalidSizes(t *testing.T) {
	_, _, cleanup := withAutoGrowFactory(t, nil, nil)
	defer cleanup()

	_, err := NewAutoGrowEnginePool(0, 0, autogrowTestType)
	require.Error(t, err)

	_, err = NewAutoGrowEnginePool(-1, 1, autogrowTestType)
	require.Error(t, err)

	_, err = NewAutoGrowEnginePool(2, 1, autogrowTestType)
	require.Error(t, err)
}

func TestNewAutoGrowEnginePool_EmptyType(t *testing.T) {
	_, _, cleanup := withAutoGrowFactory(t, nil, nil)
	defer cleanup()

	_, err := NewAutoGrowEnginePool(0, 1, "")
	require.Error(t, err)
}

func TestNewAutoGrowEnginePool_FactoryError(t *testing.T) {
	Unregister(autogrowTestType)
	require.NoError(t, Register(autogrowTestType, func() (Engine, error) {
		return nil, errors.New("factory boom")
	}))
	defer Unregister(autogrowTestType)

	_, err := NewAutoGrowEnginePool(1, 1, autogrowTestType)
	require.Error(t, err)
}

func TestNewAutoGrowEnginePool_InitError(t *testing.T) {
	Unregister(autogrowTestType)
	var mu sync.Mutex
	var created []*mockEngine
	require.NoError(t, Register(autogrowTestType, func() (Engine, error) {
		m := newMockEngine(autogrowTestType)
		m.initErr = errors.New("init boom")
		mu.Lock()
		created = append(created, m)
		mu.Unlock()
		return m, nil
	}))
	defer Unregister(autogrowTestType)

	_, err := NewAutoGrowEnginePool(2, 2, autogrowTestType)
	require.Error(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, created, 1)
	assert.Equal(t, 1, created[0].closeCount, "failed engine must be Closed")
}

func TestNewAutoGrowEnginePool_ZeroInitial(t *testing.T) {
	_, _, cleanup := withAutoGrowFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewAutoGrowEnginePool(0, 2, autogrowTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	eng, err := pool.Acquire()
	require.NoError(t, err)
	assert.NotNil(t, eng)
	pool.Release(eng)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestAutoGrowEnginePool_Acquire_GrowsUnderCap(t *testing.T) {
	created, mu, cleanup := withAutoGrowFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewAutoGrowEnginePool(0, 3, autogrowTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	a, err := pool.Acquire()
	require.NoError(t, err)
	b, err := pool.Acquire()
	require.NoError(t, err)
	c, err := pool.Acquire()
	require.NoError(t, err)
	assert.NotSame(t, a, b)
	assert.NotSame(t, a, c)

	mu.Lock()
	assert.Len(t, *created, 3)
	mu.Unlock()
}

func TestAutoGrowEnginePool_Acquire_BlocksAtCap(t *testing.T) {
	_, _, cleanup := withAutoGrowFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewAutoGrowEnginePool(1, 1, autogrowTestType)
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

func TestAutoGrowEnginePool_Release_AfterClose(t *testing.T) {
	_, _, cleanup := withAutoGrowFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewAutoGrowEnginePool(1, 1, autogrowTestType)
	require.NoError(t, err)

	eng, err := pool.Acquire()
	require.NoError(t, err)
	mock := eng.(*mockEngine)

	require.NoError(t, pool.Close())
	pool.Release(eng)
	assert.Equal(t, 1, mock.closeCount, "borrowed engine must be Closed when pool is closed")
}

func TestAutoGrowEnginePool_Close_Twice(t *testing.T) {
	_, _, cleanup := withAutoGrowFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewAutoGrowEnginePool(0, 1, autogrowTestType)
	require.NoError(t, err)

	require.NoError(t, pool.Close())
	assert.NoError(t, pool.Close())
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestAutoGrowEnginePool_SetSource_Load(t *testing.T) {
	_, _, cleanup := withAutoGrowFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewAutoGrowEnginePool(0, 2, autogrowTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	src := source.NewMemSource()
	src.Set("k", "code")
	pool.SetSource(src)

	require.NoError(t, pool.Load(context.Background(), "k"))
}

func TestAutoGrowEnginePool_ExecuteFromKey(t *testing.T) {
	_, _, cleanup := withAutoGrowFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewAutoGrowEnginePool(0, 2, autogrowTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	src := source.NewMemSource()
	src.Set("k", "code")
	pool.SetSource(src)

	got, err := pool.ExecuteFromKey(context.Background(), "k")
	require.NoError(t, err)
	assert.Equal(t, "exec-result", got)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestAutoGrowEnginePool_ConcurrentAcquireRelease(t *testing.T) {
	_, _, cleanup := withAutoGrowFactory(t, nil, nil)
	defer cleanup()

	const initial = 1
	const max = 4
	pool, err := NewAutoGrowEnginePool(initial, max, autogrowTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	const goroutines = 30
	const loops = 50
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

////////////////////////////////////////////////////////////////////////////////
//
////////////////////////////////////////////////////////////////////////////////

type fakeSandboxEngine struct {
	*mockEngine
	openLibs     []string
	injectedGlob string
	injectedVal  string
}

func newFakeSandboxEngine(libs []string) *fakeSandboxEngine {
	return &fakeSandboxEngine{
		mockEngine: newMockEngine(autogrowTestType),
		openLibs:   libs,
	}
}

func fakeSandboxFactory(t *testing.T, libs []string, created *[]*fakeSandboxEngine, mu *sync.Mutex) EngineFactoryFunc {
	t.Helper()
	return func(ctx context.Context) (Engine, error) {
		m := newFakeSandboxEngine(libs)
		m.injectedGlob = "marker"
		m.injectedVal = "injected"
		m.globals["marker"] = "injected" // simulate the pre-Init injection
		if err := m.Init(ctx); err != nil {
			return nil, err
		}
		mu.Lock()
		*created = append(*created, m)
		mu.Unlock()
		return m, nil
	}
}

func TestNewAutoGrowEnginePoolWithFactory_PreConfiguresEagerInstances(t *testing.T) {
	var (
		mu      sync.Mutex
		created []*fakeSandboxEngine
	)
	factory := fakeSandboxFactory(t, []string{"base", "string"}, &created, &mu)

	pool, err := NewAutoGrowEnginePoolWithFactory(2, 4, factory)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	mu.Lock()
	require.Len(t, created, 2, "initialSize instances must be created eagerly")
	for _, m := range created {
		assert.Equal(t, []string{"base", "string"}, m.openLibs, "sandbox allow-list must be applied")
		assert.Equal(t, "injected", m.injectedVal, "global must be injected pre-Init")
	}
	mu.Unlock()
}

func TestNewAutoGrowEnginePoolWithFactory_PreConfiguresGrownInstances(t *testing.T) {
	var (
		mu      sync.Mutex
		created []*fakeSandboxEngine
	)
	factory := fakeSandboxFactory(t, []string{"base", "string"}, &created, &mu)

	pool, err := NewAutoGrowEnginePoolWithFactory(1, 3, factory)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	a, err := pool.Acquire() // pre-allocated
	require.NoError(t, err)
	b, err := pool.Acquire() // grown
	require.NoError(t, err)
	c, err := pool.Acquire() // grown
	require.NoError(t, err)

	mu.Lock()
	require.Len(t, created, 3)
	for _, m := range created {
		assert.Equal(t, []string{"base", "string"}, m.openLibs, "grown instances must also be sandboxed")
	}
	mu.Unlock()

	pool.Release(a)
	pool.Release(b)
	pool.Release(c)
}

func TestNewAutoGrowEnginePoolWithFactory_NilFactoryRejected(t *testing.T) {
	_, err := NewAutoGrowEnginePoolWithFactory(0, 2, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "factory cannot be nil")
}

func TestNewAutoGrowEnginePoolWithFactory_InvalidSizes(t *testing.T) {
	factory := func(context.Context) (Engine, error) { return newMockEngine(autogrowTestType), nil }

	_, err := NewAutoGrowEnginePoolWithFactory(-1, 2, factory)
	require.Error(t, err)

	_, err = NewAutoGrowEnginePoolWithFactory(2, 1, factory)
	require.Error(t, err)

	_, err = NewAutoGrowEnginePoolWithFactory(0, 0, factory)
	require.Error(t, err)
}

func TestNewAutoGrowEnginePoolWithFactory_FactoryErrorRollsBack(t *testing.T) {
	calls := 0
	var first *mockEngine
	factory := func(ctx context.Context) (Engine, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("boom on second")
		}
		m := newMockEngine(autogrowTestType)
		if err := m.Init(ctx); err != nil {
			return nil, err
		}
		first = m
		return m, nil
	}

	_, err := NewAutoGrowEnginePoolWithFactory(2, 2, factory)
	require.Error(t, err)
	require.Equal(t, 2, calls, "factory must be called once per requested instance before failing")

	require.NotNil(t, first, "first instance must have been created")
	assert.Equal(t, 1, first.closeCount, "partially-built instance must be Closed on rollback")
}

func TestNewAutoGrowEnginePoolWithFactory_GrowFactoryError(t *testing.T) {
	factory := func(context.Context) (Engine, error) {
		return nil, errors.New("growth always fails")
	}

	pool, err := NewAutoGrowEnginePoolWithFactory(0, 2, factory)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	_, err = pool.Acquire() // attempts growth -> factory error
	require.Error(t, err)
}

func TestNewAutoGrowEnginePoolWithFactory_ZeroInitialGrowsOnDemand(t *testing.T) {
	var (
		mu      sync.Mutex
		created []*fakeSandboxEngine
	)
	factory := fakeSandboxFactory(t, []string{"base"}, &created, &mu)

	pool, err := NewAutoGrowEnginePoolWithFactory(0, 3, factory)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	mu.Lock()
	assert.Empty(t, created, "initialSize=0 must create nothing eagerly")
	mu.Unlock()

	eng, err := pool.Acquire()
	require.NoError(t, err)
	require.NotNil(t, eng)

	mu.Lock()
	require.Len(t, created, 1, "Acquire must lazily create one instance")
	assert.True(t, created[0].initialized, "factory-produced instance must be Init'd")
	mu.Unlock()

	pool.Release(eng)
}

func TestNewAutoGrowEnginePoolWithFactory_AcquireBlocksAtCapUntilRelease(t *testing.T) {
	var (
		mu      sync.Mutex
		created []*fakeSandboxEngine
	)
	factory := fakeSandboxFactory(t, []string{"base"}, &created, &mu)

	pool, err := NewAutoGrowEnginePoolWithFactory(0, 1, factory)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	first, err := pool.Acquire()
	require.NoError(t, err)

	acquired := make(chan Engine, 1)
	go func() {
		eng, err := pool.Acquire()
		require.NoError(t, err)
		acquired <- eng
	}()

	select {
	case <-acquired:
		t.Fatal("second Acquire must block at cap until a Release")
	case <-time.After(50 * time.Millisecond):
	}

	pool.Release(first)

	select {
	case eng := <-acquired:
		assert.NotNil(t, eng)
		mu.Lock()
		assert.Len(t, created, 1, "cap reached must not create new instances")
		mu.Unlock()
		pool.Release(eng)
	case <-time.After(time.Second):
		t.Fatal("second Acquire did not unblock after Release")
	}
}

func TestNewAutoGrowEnginePoolWithFactory_CloseReleasesIdleInstances(t *testing.T) {
	var (
		mu      sync.Mutex
		created []*fakeSandboxEngine
	)
	factory := fakeSandboxFactory(t, []string{"base"}, &created, &mu)

	pool, err := NewAutoGrowEnginePoolWithFactory(3, 3, factory)
	require.NoError(t, err)

	require.NoError(t, pool.Close())

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, created, 3)
	for _, m := range created {
		assert.Equal(t, 1, m.closeCount, "every idle instance must be Closed on pool Close")
	}
}

func TestNewAutoGrowEnginePoolWithFactory_TypeBasedWrapperProducesInitEngine(t *testing.T) {
	_, _, cleanup := withAutoGrowFactory(t, nil, nil)
	defer cleanup()

	pool, err := NewAutoGrowEnginePool(1, 2, autogrowTestType)
	require.NoError(t, err)
	defer func() { _ = pool.Close() }()

	eng, err := pool.Acquire()
	require.NoError(t, err)
	defer pool.Release(eng)

	mock, ok := eng.(*mockEngine)
	require.True(t, ok, "acquired engine must be the registered mock type")
	assert.True(t, mock.initialized, "type-based wrapper must Init the engine")
	assert.Equal(t, 1, mock.initCount, "Init must be called exactly once")
}
