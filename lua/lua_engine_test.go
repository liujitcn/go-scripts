package lua

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"

	script_engine "github.com/liujitcn/go-scripts"
	"github.com/liujitcn/go-scripts/source"
)

func TestLuaEngine(t *testing.T) {
	eng, err := newLuaEngine()
	assert.Nil(t, err)
	assert.NotNil(t, eng)
	defer eng.Close()

	ctx := context.Background()
	if err = eng.Init(ctx); err != nil {
		t.Fatal(err)
	}

	err = eng.RegisterGlobal("config", map[string]interface{}{
		"host": "localhost",
		"port": 8080,
	})

	result, err := eng.ExecuteString(ctx, "test.lua", `
    function add(a, b)
        return a + b
    end
`)
	_ = result

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err = eng.CallFunction(ctx, "add", 10, 20)
	if err != nil {
		t.Error(err)
	}
	fmt.Println(result) // expected: 30
}

func TestConcurrentCallAndGet(t *testing.T) {
	eng, err := newLuaEngine()
	assert.Nil(t, err)
	assert.NotNil(t, eng)
	defer eng.Close()

	ctx := context.Background()
	err = eng.Init(ctx)
	assert.Nil(t, err)

	_, err = eng.ExecuteString(ctx, "test.lua", `
        function add(a, b)
            return a + b
        end
    `)
	assert.Nil(t, err)

	err = eng.RegisterGlobal("config", map[string]interface{}{
		"host": "localhost",
		"port": 8080,
	})
	assert.Nil(t, err)

	var wg sync.WaitGroup
	var errCount int64

	const goroutines = 50
	const loops = 200

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < loops; j++ {
				cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				val, callErr := eng.CallFunction(cctx, "add", 10, 20)
				cancel()
				if callErr != nil {
					atomic.AddInt64(&errCount, 1)
					continue
				}

				switch v := val.(type) {
				case int:
					if v != 30 {
						atomic.AddInt64(&errCount, 1)
					}
				case int64:
					if v != 30 {
						atomic.AddInt64(&errCount, 1)
					}
				case float64:
					if v != 30.0 {
						atomic.AddInt64(&errCount, 1)
					}
				default:
					atomic.AddInt64(&errCount, 1)
				}

				gv, gerr := eng.GetGlobal("config")
				if gerr != nil {
					atomic.AddInt64(&errCount, 1)
				} else {
					if _, ok := gv.(map[string]interface{}); !ok {
						if gv == nil {
							atomic.AddInt64(&errCount, 1)
						}
					}
				}
			}
		}(i)
	}

	wg.Wait()
	if atomic.LoadInt64(&errCount) != 0 {
		t.Fatalf("concurrent operations produced %d errors, lastError=%v", atomic.LoadInt64(&errCount), eng.GetLastError())
	}
}

func TestConcurrentInitClose(t *testing.T) {
	const goroutines = 40
	const ops = 200

	eng, err := newLuaEngine()
	assert.Nil(t, err)
	assert.NotNil(t, eng)
	defer eng.Close()

	var wg sync.WaitGroup
	var initErrCount int64
	var closeErrCount int64

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				if j%2 == 0 {
					if err = eng.Init(context.Background()); err != nil {
						if !errors.Is(err, ErrLuaEngineAlreadyInitialized) {
							atomic.AddInt64(&initErrCount, 1)
						}
					}
				} else {
					if err = eng.Close(); err != nil {
						if !errors.Is(err, ErrLuaEngineNotInitialized) {
							atomic.AddInt64(&closeErrCount, 1)
						}
					}
				}
				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	wg.Wait()

	if atomic.LoadInt64(&initErrCount) != 0 || atomic.LoadInt64(&closeErrCount) != 0 {
		t.Fatalf("unexpected init/close errors: initErr=%d closeErr=%d lastError=%v",
			atomic.LoadInt64(&initErrCount), atomic.LoadInt64(&closeErrCount), eng.GetLastError())
	}

	if err = eng.Init(context.Background()); err != nil && !errors.Is(err, ErrLuaEngineAlreadyInitialized) {
		t.Fatalf("final Init failed: %v", err)
	}

	if err = eng.Close(); err != nil && !errors.Is(err, ErrLuaEngineNotInitialized) {
		t.Fatalf("final Close failed: %v", err)
	}

	fmt.Println("concurrent init/close test completed")
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func newMemSourceWith(t *testing.T, scripts map[string]string) *source.MemSource {
	t.Helper()
	src := source.NewMemSource()
	for k, v := range scripts {
		src.Set(k, v)
	}
	return src
}

func TestLuaEngine_SetGetSource(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	require.NotNil(t, eng)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	assert.Nil(t, eng.GetSource())

	want := newMemSourceWith(t, map[string]string{"k": "x"})
	eng.SetSource(want)
	assert.Same(t, want, eng.GetSource())

	eng.SetSource(nil)
	assert.Nil(t, eng.GetSource())
}

func TestLuaEngine_Load_FromSource(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	require.NotNil(t, eng)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	eng.SetSource(newMemSourceWith(t, map[string]string{
		"hello": "answer = 42",
	}))

	require.NoError(t, eng.Load(context.Background(), "hello"))

	_, err = eng.Execute(context.Background())
	require.NoError(t, err)

	v, err := eng.GetGlobal("answer")
	require.NoError(t, err)
	assert.Equal(t, int64(42), v)
}

func TestLuaEngine_Load_NoSourceBound(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	require.NotNil(t, eng)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	err = eng.Load(context.Background(), "anything")
	require.Error(t, err)
}

func TestLuaEngine_LoadMulti(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	require.NotNil(t, eng)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	eng.SetSource(newMemSourceWith(t, map[string]string{
		"a": "x = 1",
		"b": "x = 2",
	}))

	require.NoError(t, eng.LoadMulti(context.Background(), []string{"a", "b"}))

	_, err = eng.Execute(context.Background())
	require.NoError(t, err)

	v, err := eng.GetGlobal("x")
	require.NoError(t, err)
	assert.Equal(t, int64(2), v)
}

func TestLuaEngine_LoadMulti_AbortsOnError(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	require.NotNil(t, eng)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	eng.SetSource(newMemSourceWith(t, map[string]string{"a": "x = 1"}))

	err = eng.LoadMulti(context.Background(), []string{"a", "missing"})
	require.Error(t, err)
}

func TestLuaEngine_ExecuteFromKey(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	require.NotNil(t, eng)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	eng.SetSource(newMemSourceWith(t, map[string]string{
		"hello": "answer = 7",
	}))

	_, err = eng.ExecuteFromKey(context.Background(), "hello")
	require.NoError(t, err)

	v, err := eng.GetGlobal("answer")
	require.NoError(t, err)
	assert.Equal(t, int64(7), v)
}

func TestLuaEngine_ExecuteFromKeys_OrderedResults(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	require.NotNil(t, eng)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	eng.SetSource(newMemSourceWith(t, map[string]string{
		"a": "xa = 'A'",
		"b": "xb = 'B'",
	}))

	_, err = eng.ExecuteFromKeys(context.Background(), []string{"a", "b"})
	require.NoError(t, err)

	va, err := eng.GetGlobal("xa")
	require.NoError(t, err)
	assert.Equal(t, "A", va)

	vb, err := eng.GetGlobal("xb")
	require.NoError(t, err)
	assert.Equal(t, "B", vb)
}

func TestLuaEngine_LoadString_NameIgnored(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	require.NotNil(t, eng)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	require.NoError(t, eng.LoadString(context.Background(), "any-name.lua", "x = 99"))
	_, err = eng.Execute(context.Background())
	require.NoError(t, err)

	v, err := eng.GetGlobal("x")
	require.NoError(t, err)
	assert.Equal(t, int64(99), v)
}

func TestLuaEngine_Load_FromFileSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.lua")
	require.NoError(t, os.WriteFile(path, []byte("fx = 123"), 0o644))

	eng, err := newLuaEngine()
	require.NoError(t, err)
	require.NotNil(t, eng)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	eng.SetSource(source.NewFileSource())
	require.NoError(t, eng.Load(context.Background(), path))

	_, err = eng.Execute(context.Background())
	require.NoError(t, err)

	v, err := eng.GetGlobal("fx")
	require.NoError(t, err)
	assert.Equal(t, int64(123), v)
}

////////////////////////////////////////////////////////////////////////////////
// 热更新（监听）。
////////////////////////////////////////////////////////////////////////////////

func TestLuaEngine_StartWatch_HotReload(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	require.NotNil(t, eng)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	memSrc := newMemSourceWith(t, map[string]string{
		"k": "val = 1",
	})
	eng.SetSource(memSrc)

	require.NoError(t, eng.Load(context.Background(), "k"))
	_, err = eng.Execute(context.Background())
	require.NoError(t, err)
	v, err := eng.GetGlobal("val")
	require.NoError(t, err)
	assert.Equal(t, int64(1), v)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, eng.StartWatch(ctx, "k"))

	time.Sleep(50 * time.Millisecond)
	memSrc.Set("k", "val = 2")

	time.Sleep(200 * time.Millisecond)

	_, err = eng.Execute(context.Background())
	require.NoError(t, err)
	v, err = eng.GetGlobal("val")
	require.NoError(t, err)
	assert.Equal(t, int64(2), v)
}

func TestLuaEngine_StartWatch_NoSource(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	err = eng.StartWatch(context.Background(), "k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no source")
}

func TestLuaEngine_StartWatch_SourceNotWatcher(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	eng.SetSource(&readerOnly{code: "val = 1"})

	err = eng.StartWatch(context.Background(), "k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not implement Watcher")
}

func TestLuaEngine_StopWatch(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	memSrc := newMemSourceWith(t, map[string]string{"k": "val = 1"})
	eng.SetSource(memSrc)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	require.NoError(t, eng.StartWatch(ctx, "k"))
	require.NoError(t, eng.StopWatch("k"))

	memSrc.Set("k", "val = 99")
	time.Sleep(200 * time.Millisecond)

	require.NoError(t, eng.Load(context.Background(), "k"))
	_, err = eng.Execute(context.Background())
	require.NoError(t, err)
	v, err := eng.GetGlobal("val")
	require.NoError(t, err)
	assert.Equal(t, int64(99), v) // manual Load, not hot reload
}

func TestLuaEngine_Close_StopsWatchers(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	require.NoError(t, eng.Init(context.Background()))

	memSrc := newMemSourceWith(t, map[string]string{"k": "val = 1"})
	eng.SetSource(memSrc)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, eng.StartWatch(ctx, "k"))

	done := make(chan struct{})
	go func() {
		require.NoError(t, eng.Close())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked; watchers not cleaned up")
	}
}

type readerOnly struct {
	code string
}

func (r *readerOnly) Load(_ context.Context, _ string) (string, error) { return r.code, nil }
func (r *readerOnly) Close() error                                     { return nil }

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestLuaEngine_RuntimeHook_BeforeInit(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	defer eng.Close()

	var greet lua.LGFunction = func(L *lua.LState) int {
		name := L.CheckString(1)
		L.Push(lua.LString("hi " + name))
		return 1
	}
	require.NoError(t, eng.AddRuntimeHook(func(ctx context.Context) error {
		return eng.RegisterFunction("greet", greet)
	}))

	require.NoError(t, eng.Init(context.Background()))

	_, err = eng.ExecuteString(context.Background(), "hook.lua", `
        local r = greet("world")
        greet_result = r
    `)
	require.NoError(t, err)

	v, err := eng.GetGlobal("greet_result")
	require.NoError(t, err)
	assert.Equal(t, "hi world", v)
}

func TestLuaEngine_RuntimeHook_AfterInit(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	var greet2 lua.LGFunction = func(L *lua.LState) int {
		name := L.CheckString(1)
		L.Push(lua.LString("hello " + name))
		return 1
	}
	require.NoError(t, eng.AddRuntimeHook(func(ctx context.Context) error {
		return eng.RegisterFunction("greet", greet2)
	}))

	res, err := eng.CallFunction(context.Background(), "greet", "bob")
	require.NoError(t, err)
	assert.Equal(t, "hello bob", res)
}

func TestLuaEngine_RuntimeHook_PoolReuse_Isolation(t *testing.T) {
	ctx := context.Background()

	engA, err := newLuaEngine()
	require.NoError(t, err)
	require.NoError(t, engA.Init(ctx))
	require.NoError(t, engA.AddRuntimeHook(func(context.Context) error {
		return engA.RegisterGlobal("secret_a", "from-A")
	}))
	v, err := engA.GetGlobal("secret_a")
	require.NoError(t, err)
	assert.Equal(t, "from-A", v)
	require.NoError(t, engA.Close())

	engB, err := newLuaEngine()
	require.NoError(t, err)
	defer engB.Close()
	require.NoError(t, engB.Init(ctx))

	v, err = engB.GetGlobal("secret_a")
	require.NoError(t, err)
	assert.Nil(t, v, "recycled LState must not leak secret_a from engine A")

	require.NoError(t, engB.RegisterGlobal("secret_b", "from-B"))
	v, err = engB.GetGlobal("secret_b")
	require.NoError(t, err)
	assert.Equal(t, "from-B", v)

	_, err = engB.ExecuteString(ctx, "stdlib.lua", `x = string.len("abcde")`)
	require.NoError(t, err)
	v, err = engB.GetGlobal("x")
	require.NoError(t, err)
	assert.Equal(t, int64(5), v)
}

func TestLuaEngine_RuntimeHook_ReverseRegister(t *testing.T) {
	ctx := context.Background()

	hooks := make(map[string]*lua.LFunction)
	var hooksMu sync.Mutex

	eng, err := newLuaEngine()
	require.NoError(t, err)
	defer eng.Close()

	var hookRegister lua.LGFunction = func(L *lua.LState) int {
		name := L.CheckString(1)
		fn := L.CheckFunction(2)
		hooksMu.Lock()
		hooks[name] = fn
		hooksMu.Unlock()
		return 0
	}
	require.NoError(t, eng.AddRuntimeHook(func(context.Context) error {
		return eng.RegisterFunction("hook_register", hookRegister)
	}))
	require.NoError(t, eng.Init(ctx))

	_, err = eng.ExecuteString(ctx, "reg.lua", `
        hook_register("on_event", function(payload)
            return "processed:" .. payload
        end)
    `)
	require.NoError(t, err)

	hooksMu.Lock()
	fn := hooks["on_event"]
	hooksMu.Unlock()
	require.NotNil(t, fn, "Lua callback should have been registered into Go")

	var ran int32
	done := make(chan any, 1)
	go func() {
		eng.mu.Lock()
		defer eng.mu.Unlock()
		atomic.StoreInt32(&ran, 1)
		if err := eng.vm.L.CallByParam(lua.P{
			Fn:      fn,
			NRet:    1,
			Protect: true,
		}, lua.LString("data")); err != nil {
			done <- err
			return
		}
		ret := eng.vm.L.Get(-1)
		eng.vm.L.Pop(1)
		done <- eng.vm.convertFromLValue(ret)
	}()

	select {
	case res := <-done:
		require.NoError(t, nil)
		assert.Equal(t, "processed:data", res)
		assert.Equal(t, int32(1), atomic.LoadInt32(&ran))
	case <-time.After(2 * time.Second):
		t.Fatal("reverse dispatch timed out")
	}
}

func TestLuaEngine_RuntimeHook_AsCapability(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	defer eng.Close()

	r := script_engine.AsRuntimeHookRegistrar(eng)
	require.NotNil(t, r, "lua engine should implement RuntimeHookRegistrar")
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestLuaEngine_OpenLibs_DefaultOpensAll(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	_, err = eng.ExecuteString(context.Background(), "default.lua",
		`_G.os_time = os.time()`)
	require.NoError(t, err)
	v, err := eng.GetGlobal("os_time")
	require.NoError(t, err)
	assert.NotNil(t, v)

	_, err = eng.ExecuteString(context.Background(), "default2.lua",
		`_G.strlen = string.len("hello")`)
	require.NoError(t, err)
	v, err = eng.GetGlobal("strlen")
	require.NoError(t, err)
	assert.Equal(t, int64(5), v)
}

func TestLuaEngine_OpenLibs_SandboxDropsDangerous(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	defer eng.Close()

	eng.SetOpenLibs(
		AllowedLibBase, AllowedLibTab, AllowedLibStr, AllowedLibMath, AllowedLibCoroutine,
	)
	require.NoError(t, eng.Init(context.Background()))

	_, err = eng.ExecuteString(context.Background(), "sandbox.lua",
		`_G.os_type = type(os)`)
	if assert.NoError(t, err) {
		v, gerr := eng.GetGlobal("os_type")
		require.NoError(t, gerr)
		assert.Equal(t, "nil", v, "os library should be dropped in sandbox mode")
	}

	_, err = eng.ExecuteString(context.Background(), "sandbox2.lua",
		`_G.io_type = type(io)`)
	if assert.NoError(t, err) {
		v, gerr := eng.GetGlobal("io_type")
		require.NoError(t, gerr)
		assert.Equal(t, "nil", v, "io library should be dropped in sandbox mode")
	}

	_, err = eng.ExecuteString(context.Background(), "sandbox3.lua",
		`_G.strlen = string.len("hello")`)
	require.NoError(t, err)
	v, err := eng.GetGlobal("strlen")
	require.NoError(t, err)
	assert.Equal(t, int64(5), v, "string library should still work")
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestLuaEngine_ExecuteSync_Basic(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	require.NoError(t, eng.LoadString(context.Background(), "hp.lua", `hp_answer = 6 * 7`))

	_, err = eng.ExecuteSync(context.Background())
	require.NoError(t, err)

	v, err := eng.GetGlobal("hp_answer")
	require.NoError(t, err)
	assert.Equal(t, int64(42), v)
}

func TestLuaEngine_ExecuteSync_QuotaTimeout(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	require.NoError(t, eng.LoadString(context.Background(), "loop.lua",
		`local i = 0 while true do i = i + 1 end`))

	eng.SetQuota(script_engine.Quota{Timeout: 100 * time.Millisecond})

	done := make(chan error, 1)
	go func() { _, e := eng.ExecuteSync(context.Background()); done <- e }()

	select {
	case e := <-done:
		require.Error(t, e)
		assert.ErrorIs(t, e, script_engine.ErrQuotaExceeded,
			"timed-out sync run should report ErrQuotaExceeded")
	case <-time.After(3 * time.Second):
		t.Fatal("ExecuteSync hung; quota did not interrupt the loop")
	}
}

func TestLuaEngine_ExecuteSync_QuotaInstructions_UnenforcedByLua(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	require.NoError(t, eng.LoadString(context.Background(), "bounded.lua",
		`local s = 0 for i = 1, 1000 do s = s + i end hp_sum = s`))

	eng.SetQuota(script_engine.Quota{MaxInstructions: 1000}) // accepted, not enforced
	_, err = eng.ExecuteSync(context.Background())
	require.NoError(t, err)

	v, err := eng.GetGlobal("hp_sum")
	require.NoError(t, err)
	assert.Equal(t, int64(500500), v) // sum 1..1000 = 500500
}

func TestLuaEngine_ExecuteSync_QuotaClear(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	defer eng.Close()
	require.NoError(t, eng.Init(context.Background()))

	eng.SetQuota(script_engine.Quota{Timeout: 50 * time.Millisecond})
	eng.SetQuota(script_engine.Quota{}) // zero value -> no bound

	require.NoError(t, eng.LoadString(context.Background(), "hp.lua", `hp_done = 1`))
	_, err = eng.ExecuteSync(context.Background())
	require.NoError(t, err)
	v, err := eng.GetGlobal("hp_done")
	require.NoError(t, err)
	assert.Equal(t, int64(1), v)
}

func TestLuaEngine_ExecuteSync_AsCapability(t *testing.T) {
	eng, err := newLuaEngine()
	require.NoError(t, err)
	defer eng.Close()

	require.NotNil(t, script_engine.AsSyncExecutor(eng), "lua engine should implement SyncExecutor")
	require.NotNil(t, script_engine.AsQuotaController(eng), "lua engine should implement QuotaController")
}
