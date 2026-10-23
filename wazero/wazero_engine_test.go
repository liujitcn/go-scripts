package wazero

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	script_engine "github.com/liujitcn/go-scripts"
	"github.com/liujitcn/go-scripts/source"
)

var addWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x07, 0x01, 0x03, 0x61, 0x64, 0x64, 0x00, 0x00,
	0x0a, 0x09, 0x01, 0x07, 0x00, 0x20, 0x00, 0x20, 0x01, 0x6a, 0x0b,
}

var startWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x0a, 0x01, 0x06, 0x5f, 0x73, 0x74, 0x61, 0x72, 0x74, 0x00, 0x00,
	0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b,
}

var doubleWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x06, 0x01, 0x60, 0x01, 0x7f, 0x01, 0x7f,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x0a, 0x01, 0x06, 0x64, 0x6f, 0x75, 0x62, 0x6c, 0x65, 0x00, 0x00,
	0x0a, 0x09, 0x01, 0x07, 0x00, 0x20, 0x00, 0x41, 0x01, 0x6a, 0x0b,
}

var globalWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x06, 0x06, 0x01, 0x7f, 0x01, 0x41, 0x2a, 0x0b,
	0x07, 0x0b, 0x01, 0x07, 0x63, 0x6f, 0x75, 0x6e, 0x74, 0x65, 0x72, 0x03, 0x00,
}

var invalidWasm = []byte{0x00, 0x00, 0x00, 0x00}

func newTestEngine(t *testing.T) *engine {
	t.Helper()
	eng, err := newWazeroEngine()
	require.NoError(t, err)
	require.NoError(t, eng.Init(context.Background()))
	t.Cleanup(func() { _ = eng.Close() })
	return eng
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestGetType(t *testing.T) {
	eng, _ := newWazeroEngine()
	assert.Equal(t, script_engine.WazeroType, eng.GetType())
}

func TestInit(t *testing.T) {
	eng, _ := newWazeroEngine()
	err := eng.Init(context.Background())
	assert.NoError(t, err)
	assert.True(t, eng.IsInitialized())
	_ = eng.Close()
}

func TestInitTwice(t *testing.T) {
	eng := newTestEngine(t)
	err := eng.Init(context.Background())
	assert.ErrorIs(t, err, ErrWazeroEngineAlreadyInitialized)
}

func TestClose(t *testing.T) {
	eng, _ := newWazeroEngine()
	_ = eng.Init(context.Background())
	err := eng.Close()
	assert.NoError(t, err)
	assert.False(t, eng.IsInitialized())
}

func TestCloseNotInitialized(t *testing.T) {
	eng, _ := newWazeroEngine()
	err := eng.Close()
	assert.ErrorIs(t, err, ErrWazeroEngineNotInitialized)
}

func TestIsInitialized(t *testing.T) {
	eng, _ := newWazeroEngine()
	assert.False(t, eng.IsInitialized())
	_ = eng.Init(context.Background())
	assert.True(t, eng.IsInitialized())
	_ = eng.Close()
	assert.False(t, eng.IsInitialized())
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestSetGetSource(t *testing.T) {
	eng := newTestEngine(t)
	src := source.NewMemSource()
	eng.SetSource(src)
	assert.Equal(t, src, eng.GetSource())

	eng.SetSource(nil)
	assert.Nil(t, eng.GetSource())
}

func TestLoadNoSource(t *testing.T) {
	eng := newTestEngine(t)
	err := eng.Load(context.Background(), "test.wasm")
	assert.Error(t, err)
}

func TestExecuteFromKeyNoSource(t *testing.T) {
	eng := newTestEngine(t)
	_, err := eng.ExecuteFromKey(context.Background(), "test.wasm")
	assert.Error(t, err)
}

func TestExecuteFromKeysNoSource(t *testing.T) {
	eng := newTestEngine(t)
	_, err := eng.ExecuteFromKeys(context.Background(), []string{"a.wasm"})
	assert.Error(t, err)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestLoadString(t *testing.T) {
	eng := newTestEngine(t)
	err := eng.LoadString(context.Background(), "add", string(addWasm))
	assert.NoError(t, err)
}

func TestLoadStringInvalid(t *testing.T) {
	eng := newTestEngine(t)
	err := eng.LoadString(context.Background(), "bad", string(invalidWasm))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrWazeroCompileFailed)
}

func TestLoadStringNotInitialized(t *testing.T) {
	eng, _ := newWazeroEngine()
	err := eng.LoadString(context.Background(), "add", string(addWasm))
	assert.ErrorIs(t, err, ErrWazeroEngineNotInitialized)
}

func TestLoadFromSource(t *testing.T) {
	eng := newTestEngine(t)
	src := source.NewMemSource()
	src.Set("add.wasm", string(addWasm))
	eng.SetSource(src)

	err := eng.Load(context.Background(), "add.wasm")
	assert.NoError(t, err)
}

func TestLoadMulti(t *testing.T) {
	eng := newTestEngine(t)
	src := source.NewMemSource()
	src.Set("a.wasm", string(addWasm))
	src.Set("b.wasm", string(startWasm))
	eng.SetSource(src)

	err := eng.LoadMulti(context.Background(), []string{"a.wasm", "b.wasm"})
	assert.NoError(t, err)
}

func TestLoadMultiError(t *testing.T) {
	eng := newTestEngine(t)
	src := source.NewMemSource()
	src.Set("a.wasm", string(addWasm))
	eng.SetSource(src)

	err := eng.LoadMulti(context.Background(), []string{"a.wasm", "missing.wasm"})
	assert.Error(t, err)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestExecute(t *testing.T) {
	eng := newTestEngine(t)
	require.NoError(t, eng.LoadString(context.Background(), "start", string(startWasm)))

	result, err := eng.Execute(context.Background())
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestExecuteNoModule(t *testing.T) {
	eng := newTestEngine(t)
	_, err := eng.Execute(context.Background())
	assert.ErrorIs(t, err, ErrWazeroNoModuleLoaded)
}

func TestExecuteNotInitialized(t *testing.T) {
	eng, _ := newWazeroEngine()
	_, err := eng.Execute(context.Background())
	assert.ErrorIs(t, err, ErrWazeroEngineNotInitialized)
}

func TestExecuteString(t *testing.T) {
	eng := newTestEngine(t)
	result, err := eng.ExecuteString(context.Background(), "start", string(startWasm))
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestExecuteStringInvalid(t *testing.T) {
	eng := newTestEngine(t)
	_, err := eng.ExecuteString(context.Background(), "bad", string(invalidWasm))
	assert.ErrorIs(t, err, ErrWazeroCompileFailed)
}

func TestExecuteStringNotInitialized(t *testing.T) {
	eng, _ := newWazeroEngine()
	_, err := eng.ExecuteString(context.Background(), "start", string(startWasm))
	assert.ErrorIs(t, err, ErrWazeroEngineNotInitialized)
}

func TestExecuteFromKey(t *testing.T) {
	eng := newTestEngine(t)
	src := source.NewMemSource()
	src.Set("start.wasm", string(startWasm))
	eng.SetSource(src)

	result, err := eng.ExecuteFromKey(context.Background(), "start.wasm")
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestExecuteFromKeys(t *testing.T) {
	eng := newTestEngine(t)
	src := source.NewMemSource()
	src.Set("a.wasm", string(startWasm))
	src.Set("b.wasm", string(addWasm))
	eng.SetSource(src)

	results, err := eng.ExecuteFromKeys(context.Background(), []string{"a.wasm", "b.wasm"})
	assert.NoError(t, err)
	assert.Len(t, results, 2)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestCallFunction(t *testing.T) {
	eng := newTestEngine(t)
	require.NoError(t, eng.LoadString(context.Background(), "add", string(addWasm)))
	_, err := eng.Execute(context.Background())
	require.NoError(t, err)

	result, err := eng.CallFunction(context.Background(), "add", uint64(3), uint64(4))
	assert.NoError(t, err)
	assert.Equal(t, uint64(7), result)
}

func TestCallFunctionIntArgs(t *testing.T) {
	eng := newTestEngine(t)
	require.NoError(t, eng.LoadString(context.Background(), "add", string(addWasm)))
	_, err := eng.Execute(context.Background())
	require.NoError(t, err)

	result, err := eng.CallFunction(context.Background(), "add", int(10), int(20))
	assert.NoError(t, err)
	assert.Equal(t, uint64(30), result)
}

func TestCallFunctionDouble(t *testing.T) {
	eng := newTestEngine(t)
	require.NoError(t, eng.LoadString(context.Background(), "double", string(doubleWasm)))
	_, err := eng.Execute(context.Background())
	require.NoError(t, err)

	result, err := eng.CallFunction(context.Background(), "double", uint64(5))
	assert.NoError(t, err)
	assert.Equal(t, uint64(6), result)
}

func TestCallFunctionNotFound(t *testing.T) {
	eng := newTestEngine(t)
	require.NoError(t, eng.LoadString(context.Background(), "add", string(addWasm)))
	_, err := eng.Execute(context.Background())
	require.NoError(t, err)

	_, err = eng.CallFunction(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, ErrWazeroFunctionNotFound)
}

func TestCallFunctionNotInstantiated(t *testing.T) {
	eng := newTestEngine(t)
	_, err := eng.CallFunction(context.Background(), "add")
	assert.ErrorIs(t, err, ErrWazeroNotInstantiated)
}

func TestCallFunctionNotInitialized(t *testing.T) {
	eng, _ := newWazeroEngine()
	_, err := eng.CallFunction(context.Background(), "add")
	assert.ErrorIs(t, err, ErrWazeroEngineNotInitialized)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestGetGlobal(t *testing.T) {
	eng := newTestEngine(t)
	require.NoError(t, eng.LoadString(context.Background(), "global", string(globalWasm)))
	_, err := eng.Execute(context.Background())
	require.NoError(t, err)

	val, err := eng.GetGlobal("counter")
	assert.NoError(t, err)
	assert.Equal(t, uint64(42), val)
}

func TestGetGlobalNotFound(t *testing.T) {
	eng := newTestEngine(t)
	require.NoError(t, eng.LoadString(context.Background(), "global", string(globalWasm)))
	_, err := eng.Execute(context.Background())
	require.NoError(t, err)

	_, err = eng.GetGlobal("missing")
	assert.Error(t, err)
}

func TestGetGlobalNotInstantiated(t *testing.T) {
	eng := newTestEngine(t)
	_, err := eng.GetGlobal("counter")
	assert.ErrorIs(t, err, ErrWazeroNotInstantiated)
}

func TestRegisterGlobal(t *testing.T) {
	eng := newTestEngine(t)
	err := eng.RegisterGlobal("x", 42)
	assert.NoError(t, err)
}

func TestRegisterGlobalNotInitialized(t *testing.T) {
	eng, _ := newWazeroEngine()
	err := eng.RegisterGlobal("x", 42)
	assert.ErrorIs(t, err, ErrWazeroEngineNotInitialized)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestRegisterFunction(t *testing.T) {
	eng := newTestEngine(t)
	err := eng.RegisterFunction("double", func(ctx context.Context, n uint64) uint64 {
		return n * 2
	})
	assert.NoError(t, err)
}

func TestRegisterFunctionOverwrite(t *testing.T) {
	eng := newTestEngine(t)
	err := eng.RegisterFunction("fn", func(ctx context.Context, n uint64) uint64 {
		return n + 1
	})
	assert.NoError(t, err)

	err = eng.RegisterFunction("fn", func(ctx context.Context, n uint64) uint64 {
		return n * 3
	})
	assert.NoError(t, err)
}

func TestRegisterFunctionNotInitialized(t *testing.T) {
	eng, _ := newWazeroEngine()
	err := eng.RegisterFunction("fn", func() {})
	assert.ErrorIs(t, err, ErrWazeroEngineNotInitialized)
}

func TestRegisterModule(t *testing.T) {
	eng := newTestEngine(t)
	err := eng.RegisterModule("math", map[string]any{
		"add": func(ctx context.Context, a, b uint64) uint64 { return a + b },
	})
	assert.NoError(t, err)
}

func TestRegisterModuleBadType(t *testing.T) {
	eng := newTestEngine(t)
	err := eng.RegisterModule("bad", "not a map")
	assert.Error(t, err)
}

func TestRegisterModuleNotInitialized(t *testing.T) {
	eng, _ := newWazeroEngine()
	err := eng.RegisterModule("math", map[string]any{})
	assert.ErrorIs(t, err, ErrWazeroEngineNotInitialized)
}

////////////////////////////////////////////////////////////////////////////////
// 错误处理。
////////////////////////////////////////////////////////////////////////////////

func TestLastError(t *testing.T) {
	eng := newTestEngine(t)

	assert.NoError(t, eng.GetLastError())

	_ = eng.LoadString(context.Background(), "bad", string(invalidWasm))
	assert.Error(t, eng.GetLastError())

	eng.ClearError()
	assert.NoError(t, eng.GetLastError())
}

func TestClearError(t *testing.T) {
	eng := newTestEngine(t)
	_ = eng.LoadString(context.Background(), "bad", string(invalidWasm))
	assert.Error(t, eng.GetLastError())

	eng.ClearError()
	assert.NoError(t, eng.GetLastError())
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestFactoryRegistration(t *testing.T) {
	factory, ok := script_engine.GetFactory(script_engine.WazeroType)
	assert.True(t, ok)
	assert.NotNil(t, factory)
}

func TestFactoryCreateAndUse(t *testing.T) {
	eng, err := script_engine.NewScriptEngine(script_engine.WazeroType)
	assert.NoError(t, err)
	require.NotNil(t, eng)

	require.NoError(t, eng.Init(context.Background()))
	defer eng.Close()

	require.NoError(t, eng.LoadString(context.Background(), "add", string(addWasm)))
	_, err = eng.Execute(context.Background())
	assert.NoError(t, err)

	result, err := eng.CallFunction(context.Background(), "add", uint64(1), uint64(2))
	assert.NoError(t, err)
	assert.Equal(t, uint64(3), result)
}

////////////////////////////////////////////////////////////////////////////////
// 热更新（监听）。
////////////////////////////////////////////////////////////////////////////////

func TestStartWatchNoSource(t *testing.T) {
	eng := newTestEngine(t)
	err := eng.StartWatch(context.Background(), "test.wasm")
	assert.Error(t, err)
}

func TestStartWatchSourceNotWatcher(t *testing.T) {
	eng := newTestEngine(t)
	eng.SetSource(nonWatcherSource{})

	err := eng.StartWatch(context.Background(), "test.wasm")
	assert.Error(t, err)
}

func TestStopWatchNoOp(t *testing.T) {
	eng := newTestEngine(t)
	err := eng.StopWatch("nonexistent")
	assert.NoError(t, err)
}

func TestStartStopWatchWithMem(t *testing.T) {
	eng := newTestEngine(t)
	src := source.NewMemSource()
	src.Set("watch.wasm", string(addWasm))
	eng.SetSource(src)

	err := eng.StartWatch(context.Background(), "watch.wasm")
	assert.NoError(t, err)

	src.Set("watch.wasm", string(doubleWasm))
	time.Sleep(50 * time.Millisecond)

	err = eng.StopWatch("watch.wasm")
	assert.NoError(t, err)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestConcurrentExecuteCall(t *testing.T) {
	eng := newTestEngine(t)
	require.NoError(t, eng.LoadString(context.Background(), "add", string(addWasm)))
	_, err := eng.Execute(context.Background())
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			result, err := eng.CallFunction(context.Background(), "add", uint64(n), uint64(1))
			assert.NoError(t, err)
			assert.NotNil(t, result)
		}(i)
	}
	wg.Wait()
}

func TestConcurrentLoadExecute(t *testing.T) {
	eng := newTestEngine(t)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = eng.LoadString(context.Background(), "mod", string(addWasm))
		}()
	}
	wg.Wait()

	_, err := eng.Execute(context.Background())
	assert.NoError(t, err)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

type nonWatcherSource struct{}

func (nonWatcherSource) Load(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("not implemented")
}
func (nonWatcherSource) Close() error { return nil }
