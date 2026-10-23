package yaegi

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/traefik/yaegi/interp"

	script_engine "github.com/liujitcn/go-scripts"
	"github.com/liujitcn/go-scripts/source"
)

func newTestEngine(t *testing.T) *engine {
	t.Helper()
	eng, err := newYaegiEngine()
	require.NoError(t, err)
	require.NoError(t, eng.Init(context.Background()))
	return eng
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestGetType(t *testing.T) {
	eng, err := newYaegiEngine()
	require.NoError(t, err)
	assert.Equal(t, script_engine.YaegiType, eng.GetType())
}

func TestInit(t *testing.T) {
	eng, err := newYaegiEngine()
	require.NoError(t, err)
	require.NoError(t, eng.Init(context.Background()))
	assert.True(t, eng.IsInitialized())
}

func TestInit_AlreadyInitialized(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	err := eng.Init(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrYaegiEngineAlreadyInitialized)
}

func TestClose(t *testing.T) {
	eng := newTestEngine(t)
	require.NoError(t, eng.Close())
	assert.False(t, eng.IsInitialized())
}

func TestClose_NotInitialized(t *testing.T) {
	eng, err := newYaegiEngine()
	require.NoError(t, err)
	err = eng.Close()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrYaegiEngineNotInitialized)
}

func TestOperations_NotInitialized(t *testing.T) {
	eng, err := newYaegiEngine()
	require.NoError(t, err)

	assert.ErrorIs(t, eng.Load(context.Background(), "k"), ErrYaegiEngineNotInitialized)

	_, err = eng.Execute(context.Background())
	assert.ErrorIs(t, err, ErrYaegiEngineNotInitialized)

	_, err = eng.ExecuteString(context.Background(), "n", "1+1")
	assert.ErrorIs(t, err, ErrYaegiEngineNotInitialized)

	assert.ErrorIs(t, eng.RegisterGlobal("x", 1), ErrYaegiEngineNotInitialized)

	_, err = eng.GetGlobal("x")
	assert.ErrorIs(t, err, ErrYaegiEngineNotInitialized)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestExecuteString_SimpleExpr(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	result, err := eng.ExecuteString(context.Background(), "test", "1 + 2")
	require.NoError(t, err)
	assert.Equal(t, 3, result)
}

func TestExecuteString_StringExpr(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	result, err := eng.ExecuteString(context.Background(), "test", `"hello" + " " + "world"`)
	require.NoError(t, err)
	assert.Equal(t, "hello world", result)
}

func TestExecuteString_VarDeclaration(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	_, err := eng.ExecuteString(context.Background(), "test", `x := 42`)
	require.NoError(t, err)

	val, err := eng.GetGlobal("x")
	require.NoError(t, err)
	assert.Equal(t, 42, val)
}

func TestExecuteString_FunctionDefAndCall(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	_, err := eng.ExecuteString(context.Background(), "test",
		`func add(a, b int) int { return a + b }`)
	require.NoError(t, err)

	result, err := eng.CallFunction(context.Background(), "add", 3, 4)
	require.NoError(t, err)
	assert.Equal(t, 7, result)
}

func TestExecuteString_InvalidCode(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	_, err := eng.ExecuteString(context.Background(), "test", `not valid go code !!!`)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrYaegiEvalFailed)
}

func TestExecuteString_PanicRecovery(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	_, err := eng.ExecuteString(context.Background(), "test", `panic("test panic")`)
	require.Error(t, err)
}

func TestExecute_NoScriptLoaded(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	_, err := eng.Execute(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrYaegiNoScriptLoaded)
}

func TestLoadString_ThenExecute(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	require.NoError(t, eng.LoadString(context.Background(), "a", `a := 10`))
	require.NoError(t, eng.LoadString(context.Background(), "b", `b := 20`))

	result, err := eng.Execute(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 20, result)

	v, err := eng.GetGlobal("a")
	require.NoError(t, err)
	assert.Equal(t, 10, v)

	v, err = eng.GetGlobal("b")
	require.NoError(t, err)
	assert.Equal(t, 20, v)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestExecuteString_ContextCanceled(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := eng.ExecuteString(ctx, "test", "1+1")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestExecuteString_ContextTimeout(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond) // ensure expired

	_, err := eng.ExecuteString(ctx, "test", "1+1")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestSetGetSource(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	mem := source.NewMemSource()
	mem.Set("test.go", "42")

	eng.SetSource(mem)
	assert.NotNil(t, eng.GetSource())

	eng.SetSource(nil)
	assert.Nil(t, eng.GetSource())
}

func TestExecuteFromKey(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	mem := source.NewMemSource()
	mem.Set("expr.go", "6 * 7")
	eng.SetSource(mem)

	result, err := eng.ExecuteFromKey(context.Background(), "expr.go")
	require.NoError(t, err)
	assert.Equal(t, 42, result)
}

func TestExecuteFromKey_NoSource(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	_, err := eng.ExecuteFromKey(context.Background(), "test.go")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no source bound")
}

func TestExecuteFromKeys(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	mem := source.NewMemSource()
	mem.Set("a.go", "10")
	mem.Set("b.go", "20")
	eng.SetSource(mem)

	results, err := eng.ExecuteFromKeys(context.Background(), []string{"a.go", "b.go"})
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, 10, results[0])
	assert.Equal(t, 20, results[1])
}

func TestLoad_FromSource(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	mem := source.NewMemSource()
	mem.Set("val.go", "99")
	eng.SetSource(mem)

	require.NoError(t, eng.Load(context.Background(), "val.go"))

	result, err := eng.Execute(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 99, result)
}

func TestLoad_SourceError(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	mem := source.NewMemSource()
	eng.SetSource(mem)

	err := eng.Load(context.Background(), "missing.go")
	require.Error(t, err)
}

func TestLoadMulti(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	mem := source.NewMemSource()
	mem.Set("a.go", "1")
	mem.Set("b.go", "2")
	eng.SetSource(mem)

	err := eng.LoadMulti(context.Background(), []string{"a.go", "b.go"})
	require.NoError(t, err)

	result, err := eng.Execute(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, result)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestRegisterGlobal(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	require.NoError(t, eng.RegisterGlobal("MyVar", 123))

	_, err := eng.ExecuteString(context.Background(), "test", `y := 99`)
	require.NoError(t, err)

	val, err := eng.GetGlobal("y")
	require.NoError(t, err)
	assert.Equal(t, 99, val)
}

func TestRegisterFunction(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	mul := func(a, b int) int { return a * b }
	require.NoError(t, eng.RegisterFunction("Mul", mul))

	_, err := eng.ExecuteString(context.Background(), "test",
		`func add(a, b int) int { return a + b }`)
	require.NoError(t, err)

	result, err := eng.CallFunction(context.Background(), "add", 3, 4)
	require.NoError(t, err)
	assert.Equal(t, 7, result)
}

func TestRegisterFunction_InvalidType(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	err := eng.RegisterFunction("notAFunc", 42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expects a func type")
}

func TestCallFunction_NotFound(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	_, err := eng.CallFunction(context.Background(), "NonExistent", 1, 2)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrYaegiFunctionNotFound)
}

func TestRegisterModule(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	mathFns := map[string]any{
		"Double": func(x int) int { return x * 2 },
	}
	require.NoError(t, eng.RegisterModule("mymath", mathFns))

}

func TestRegisterModule_InvalidType(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	err := eng.RegisterModule("bad", "not a map")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expects map[string]any")
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestLastError(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	assert.NoError(t, eng.GetLastError())

	_, err := eng.ExecuteString(context.Background(), "test", `invalid !!!`)
	require.Error(t, err)
	assert.Error(t, eng.GetLastError())

	eng.ClearError()
	assert.NoError(t, eng.GetLastError())
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestStartWatch_HotReload(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	mem := source.NewMemSource()
	mem.Set("script.go", "1")
	eng.SetSource(mem)

	require.NoError(t, eng.Load(context.Background(), "script.go"))

	require.NoError(t, eng.StartWatch(context.Background(), "script.go"))

	time.Sleep(50 * time.Millisecond)
	mem.Set("script.go", "2")

	time.Sleep(200 * time.Millisecond)

	eng.mu.RLock()
	scripts := make([]string, len(eng.scripts))
	copy(scripts, eng.scripts)
	eng.mu.RUnlock()

	require.GreaterOrEqual(t, len(scripts), 2)
	assert.Equal(t, "2", scripts[len(scripts)-1])
}

func TestStartWatch_NoSource(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	err := eng.StartWatch(context.Background(), "test.go")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no source bound")
}

func TestStartWatch_SourceNotWatcher(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	eng.SetSource(&readerOnly{})
	err := eng.StartWatch(context.Background(), "test.go")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not implement Watcher")
}

func TestStopWatch(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	mem := source.NewMemSource()
	mem.Set("script.go", "1")
	eng.SetSource(mem)

	require.NoError(t, eng.Load(context.Background(), "script.go"))
	require.NoError(t, eng.StartWatch(context.Background(), "script.go"))

	require.NoError(t, eng.StopWatch("script.go"))

	time.Sleep(50 * time.Millisecond)
	mem.Set("script.go", "99")
	time.Sleep(200 * time.Millisecond)

	eng.mu.RLock()
	scripts := eng.scripts
	eng.mu.RUnlock()

	require.Len(t, scripts, 1)
	assert.Equal(t, "1", scripts[0])
}

func TestClose_StopsWatchers(t *testing.T) {
	eng := newTestEngine(t)

	mem := source.NewMemSource()
	mem.Set("script.go", "1")
	eng.SetSource(mem)

	require.NoError(t, eng.Load(context.Background(), "script.go"))
	require.NoError(t, eng.StartWatch(context.Background(), "script.go"))

	require.NoError(t, eng.Close())

	eng.watchersMu.Lock()
	n := len(eng.watchers)
	eng.watchersMu.Unlock()
	assert.Equal(t, 0, n)
}

type readerOnly struct {
	mu   sync.Mutex
	data map[string]string
}

func (r *readerOnly) Load(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.data[key]
	if !ok {
		return "", nil
	}
	return v, nil
}

func (r *readerOnly) Close() error { return nil }

func TestExecuteString_MultiStatementScript(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	code := strings.Join([]string{
		`x := 10`,
		`y := 20`,
		`z := x + y`,
		`z`,
	}, "; ")

	result, err := eng.ExecuteString(context.Background(), "multi", code)
	require.NoError(t, err)
	assert.Equal(t, 30, result)
}

func TestDebugYaegiUseImport(t *testing.T) {
	i := interp.New(interp.Options{})
	i.Use(interp.Exports{"host": map[string]reflect.Value{
		"X": reflect.ValueOf(42),
	}})

	v1, err1 := i.Eval(`import "host"`)
	t.Logf("import result: %v, error: %v", v1, err1)

	v2, err2 := i.Eval("host.X")
	t.Logf("eval result: %v, error: %v", v2, err2)

}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestYaegiEngine_RuntimeHook_BeforeInit(t *testing.T) {
	eng, err := newYaegiEngine()
	require.NoError(t, err)
	defer eng.Close()

	var ran int32
	require.NoError(t, eng.AddRuntimeHook(func(ctx context.Context) error {
		atomic.AddInt32(&ran, 1)
		return eng.RegisterFunction("Greet", func(name string) string {
			return "hi " + name
		})
	}))

	require.NoError(t, eng.Init(context.Background()))
	assert.Equal(t, int32(1), atomic.LoadInt32(&ran), "hook should have run during Init")

	_, err = eng.ExecuteString(context.Background(), "test",
		`func add(a, b int) int { return a + b }`)
	require.NoError(t, err)
	res, err := eng.CallFunction(context.Background(), "add", 3, 4)
	require.NoError(t, err)
	assert.Equal(t, 7, res)
}

func TestYaegiEngine_RuntimeHook_AfterInit(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	var ran int32
	require.NoError(t, eng.AddRuntimeHook(func(ctx context.Context) error {
		atomic.AddInt32(&ran, 1)
		return eng.RegisterFunction("Greet", func(name string) string {
			return "hello " + name
		})
	}))
	assert.Equal(t, int32(1), atomic.LoadInt32(&ran), "hook should run immediately after Init")
}

func TestYaegiEngine_RuntimeHook_AsCapability(t *testing.T) {
	eng, err := newYaegiEngine()
	require.NoError(t, err)
	defer eng.Close()

	r := script_engine.AsRuntimeHookRegistrar(eng)
	require.NotNil(t, r, "yaegi engine should implement RuntimeHookRegistrar")
}
