package starlark

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"go.starlark.net/starlark"

	script_engine "github.com/liujitcn/go-scripts"
	"github.com/liujitcn/go-scripts/source"
)

func init() {
	_ = script_engine.Register(script_engine.StarlarkType, func() (script_engine.Engine, error) {
		return newStarlarkEngine()
	})
}

type scriptEntry struct {
	name string
	src  string
}

//

type engine struct {
	hostPredeclared starlark.StringDict

	scriptGlobals starlark.StringDict

	scripts []scriptEntry

	hostFuncs map[string]any

	source      source.Reader
	initialized bool
	lastError   error

	mu          sync.RWMutex // protects initialized, scripts, source, hostFuncs, hostPredeclared
	execMu      sync.Mutex   // protects scriptGlobals, thread execution
	lastErrorMu sync.RWMutex // protects lastError

	// 热更新状态。
	watchers   map[string]context.CancelFunc
	watchersMu sync.Mutex
}

func newStarlarkEngine() (*engine, error) {
	return &engine{
		initialized:     false,
		watchers:        make(map[string]context.CancelFunc),
		hostPredeclared: make(starlark.StringDict),
		scriptGlobals:   make(starlark.StringDict),
		hostFuncs:       make(map[string]any),
	}, nil
}

func (e *engine) GetType() script_engine.Type {
	return script_engine.StarlarkType
}

func (e *engine) Init(_ context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.initialized {
		e.setLastError(ErrStarlarkEngineAlreadyInitialized)
		return ErrStarlarkEngineAlreadyInitialized
	}

	e.hostPredeclared = make(starlark.StringDict)
	e.scriptGlobals = make(starlark.StringDict)
	e.hostFuncs = make(map[string]any)
	e.scripts = nil
	e.initialized = true
	e.lastError = nil

	return nil
}

func (e *engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.initialized {
		e.setLastError(ErrStarlarkEngineNotInitialized)
		return ErrStarlarkEngineNotInitialized
	}

	e.stopAllWatchers()

	e.execMu.Lock()
	e.hostPredeclared = nil
	e.scriptGlobals = nil
	e.scripts = nil
	e.hostFuncs = nil
	e.initialized = false
	e.execMu.Unlock()

	e.lastErrorMu.Lock()
	e.lastError = nil
	e.lastErrorMu.Unlock()

	return nil
}

func (e *engine) IsInitialized() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.initialized
}

////////////////////////////////////////////////////////////////////////////////
// 脚本源绑定与访问。
////////////////////////////////////////////////////////////////////////////////

func (e *engine) SetSource(src source.Reader) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.source = src
}

func (e *engine) GetSource() source.Reader {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.source
}

////////////////////////////////////////////////////////////////////////////////
// 脚本加载。
////////////////////////////////////////////////////////////////////////////////

func (e *engine) Load(ctx context.Context, key string) error {
	if !e.IsInitialized() {
		e.setLastError(ErrStarlarkEngineNotInitialized)
		return ErrStarlarkEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("starlark engine: no source bound")
		e.setLastError(err)
		return err
	}
	code, err := src.Load(ctx, key)
	if err != nil {
		e.setLastError(err)
		return err
	}
	return e.LoadString(ctx, key, code)
}

func (e *engine) LoadMulti(ctx context.Context, keys []string) error {
	for _, k := range keys {
		if err := e.Load(ctx, k); err != nil {
			return err
		}
	}
	return nil
}

func (e *engine) LoadString(_ context.Context, name string, code string) error {
	if !e.IsInitialized() {
		e.setLastError(ErrStarlarkEngineNotInitialized)
		return ErrStarlarkEngineNotInitialized
	}

	e.mu.Lock()
	e.scripts = append(e.scripts, scriptEntry{name: name, src: code})
	e.mu.Unlock()

	e.ClearError()
	return nil
}

////////////////////////////////////////////////////////////////////////////////
// 脚本执行。
////////////////////////////////////////////////////////////////////////////////

func (e *engine) Execute(_ context.Context) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrStarlarkEngineNotInitialized)
		return nil, ErrStarlarkEngineNotInitialized
	}

	e.mu.RLock()
	if len(e.scripts) == 0 {
		e.mu.RUnlock()
		e.setLastError(ErrStarlarkNoScriptLoaded)
		return nil, ErrStarlarkNoScriptLoaded
	}
	scripts := make([]scriptEntry, len(e.scripts))
	copy(scripts, e.scripts)
	hostPredeclared := make(starlark.StringDict, len(e.hostPredeclared))
	for k, v := range e.hostPredeclared {
		hostPredeclared[k] = v
	}
	e.mu.RUnlock()

	e.execMu.Lock()
	defer e.execMu.Unlock()

	for _, s := range scripts {

		predeclared := make(starlark.StringDict)
		for k, v := range hostPredeclared {
			predeclared[k] = v
		}
		for k, v := range e.scriptGlobals {
			predeclared[k] = v
		}

		thread := &starlark.Thread{Name: s.name}
		globals, err := starlark.ExecFile(thread, s.name, s.src, predeclared)
		if err != nil {
			wrapped := fmt.Errorf("%w: %s: %v", ErrStarlarkExecFailed, s.name, err)
			e.setLastError(wrapped)
			return nil, wrapped
		}

		for k, v := range globals {
			e.scriptGlobals[k] = v
		}
	}

	e.ClearError()
	return nil, nil
}

func (e *engine) ExecuteFromKey(ctx context.Context, key string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrStarlarkEngineNotInitialized)
		return nil, ErrStarlarkEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("starlark engine: no source bound")
		e.setLastError(err)
		return nil, err
	}
	code, err := src.Load(ctx, key)
	if err != nil {
		e.setLastError(err)
		return nil, err
	}
	return e.ExecuteString(ctx, key, code)
}

func (e *engine) ExecuteFromKeys(ctx context.Context, keys []string) ([]any, error) {
	results := make([]any, 0, len(keys))
	for _, k := range keys {
		res, err := e.ExecuteFromKey(ctx, k)
		if err != nil {
			return nil, err
		}
		results = append(results, res)
	}
	return results, nil
}

func (e *engine) ExecuteString(_ context.Context, name string, code string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrStarlarkEngineNotInitialized)
		return nil, ErrStarlarkEngineNotInitialized
	}

	e.mu.RLock()
	hostPredeclared := make(starlark.StringDict, len(e.hostPredeclared))
	for k, v := range e.hostPredeclared {
		hostPredeclared[k] = v
	}
	e.mu.RUnlock()

	e.execMu.Lock()
	defer e.execMu.Unlock()

	predeclared := make(starlark.StringDict)
	for k, v := range hostPredeclared {
		predeclared[k] = v
	}
	for k, v := range e.scriptGlobals {
		predeclared[k] = v
	}

	thread := &starlark.Thread{Name: name}
	globals, err := starlark.ExecFile(thread, name, code, predeclared)
	if err != nil {
		wrapped := fmt.Errorf("%w: %s: %v", ErrStarlarkExecFailed, name, err)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	for k, v := range globals {
		e.scriptGlobals[k] = v
	}

	e.ClearError()
	return nil, nil
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

func (e *engine) RegisterGlobal(name string, value any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrStarlarkEngineNotInitialized)
		return ErrStarlarkEngineNotInitialized
	}

	val := goToStarlark(value)

	e.mu.Lock()
	e.hostPredeclared[name] = val
	e.mu.Unlock()

	e.ClearError()
	return nil
}

func (e *engine) GetGlobal(name string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrStarlarkEngineNotInitialized)
		return nil, ErrStarlarkEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()

	if v, ok := e.scriptGlobals[name]; ok {
		e.ClearError()
		return starlarkToGo(v), nil
	}

	e.mu.RLock()
	v, ok := e.hostPredeclared[name]
	e.mu.RUnlock()
	if !ok {
		wrapped := fmt.Errorf("%w: %s", ErrStarlarkGlobalNotFound, name)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	e.ClearError()
	return starlarkToGo(v), nil
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

func (e *engine) RegisterFunction(name string, fn any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrStarlarkEngineNotInitialized)
		return ErrStarlarkEngineNotInitialized
	}

	if reflect.TypeOf(fn).Kind() != reflect.Func {
		err := fmt.Errorf("starlark engine: RegisterFunction expects a func type, got %T", fn)
		e.setLastError(err)
		return err
	}

	e.mu.Lock()
	e.hostFuncs[name] = fn
	builtin := starlark.NewBuiltin(name, func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		return callHostFuncFromStarlark(fn, args)
	})
	e.hostPredeclared[name] = builtin
	e.mu.Unlock()

	e.ClearError()
	return nil
}

func (e *engine) CallFunction(_ context.Context, name string, args ...any) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrStarlarkEngineNotInitialized)
		return nil, ErrStarlarkEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()

	fn, ok := e.scriptGlobals[name]
	if !ok {
		e.mu.RLock()
		fn, ok = e.hostPredeclared[name]
		e.mu.RUnlock()
	}
	if !ok || fn == nil {
		wrapped := fmt.Errorf("%w: %s", ErrStarlarkFunctionNotFound, name)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	e.mu.RLock()
	hostFn, isHost := e.hostFuncs[name]
	e.mu.RUnlock()
	if isHost {
		result := callHostFunc(hostFn, args)
		e.ClearError()
		return result, nil
	}

	callable, callOk := fn.(starlark.Callable)
	if !callOk {
		wrapped := fmt.Errorf("%w: %s is not callable", ErrStarlarkCallFailed, name)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	slArgs := make(starlark.Tuple, len(args))
	for i, a := range args {
		slArgs[i] = goToStarlark(a)
	}

	thread := &starlark.Thread{Name: "call_" + name}
	result, err := starlark.Call(thread, callable, slArgs, nil)
	if err != nil {
		wrapped := fmt.Errorf("%w: %s: %v", ErrStarlarkCallFailed, name, err)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	e.ClearError()
	return starlarkToGo(result), nil
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

func (e *engine) RegisterModule(name string, module any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrStarlarkEngineNotInitialized)
		return ErrStarlarkEngineNotInitialized
	}

	syms, ok := module.(map[string]any)
	if !ok {
		err := fmt.Errorf("starlark engine: RegisterModule expects map[string]any, got %T", module)
		e.setLastError(err)
		return err
	}

	e.mu.Lock()
	for key, val := range syms {
		globalName := name + "_" + key
		e.hostPredeclared[globalName] = goToStarlark(val)
	}
	e.mu.Unlock()

	e.ClearError()
	return nil
}

////////////////////////////////////////////////////////////////////////////////
// 错误处理。
////////////////////////////////////////////////////////////////////////////////

func (e *engine) GetLastError() error {
	e.lastErrorMu.RLock()
	defer e.lastErrorMu.RUnlock()
	return e.lastError
}

func (e *engine) setLastError(err error) {
	e.lastErrorMu.Lock()
	defer e.lastErrorMu.Unlock()
	e.lastError = err
}

func (e *engine) ClearError() {
	e.lastErrorMu.Lock()
	defer e.lastErrorMu.Unlock()
	e.lastError = nil
}

////////////////////////////////////////////////////////////////////////////////
// 热更新（监听）。
////////////////////////////////////////////////////////////////////////////////

func (e *engine) StartWatch(ctx context.Context, key string) error {
	if !e.IsInitialized() {
		e.setLastError(ErrStarlarkEngineNotInitialized)
		return ErrStarlarkEngineNotInitialized
	}

	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("starlark engine: no source bound")
		e.setLastError(err)
		return err
	}

	watcher, ok := src.(source.Watcher)
	if !ok {
		err := fmt.Errorf("starlark engine: source does not implement Watcher")
		e.setLastError(err)
		return err
	}

	e.StopWatch(key)

	watchCtx, cancel := context.WithCancel(ctx)

	e.watchersMu.Lock()
	e.watchers[key] = cancel
	e.watchersMu.Unlock()

	go func() {
		ch, err := watcher.Watch(watchCtx, key)
		if err != nil {
			cancel()
			return
		}
		for range ch {
			if loadErr := e.Load(watchCtx, key); loadErr != nil {
				script_engine.GetLogger().Warn(watchCtx, "starlark engine: hot reload failed",
					"key", key, "error", loadErr)
			}
		}
	}()

	return nil
}

func (e *engine) StopWatch(key string) error {
	e.watchersMu.Lock()
	defer e.watchersMu.Unlock()
	if cancel, ok := e.watchers[key]; ok {
		cancel()
		delete(e.watchers, key)
	}
	return nil
}

func (e *engine) stopAllWatchers() {
	e.watchersMu.Lock()
	defer e.watchersMu.Unlock()
	for key, cancel := range e.watchers {
		cancel()
		delete(e.watchers, key)
	}
}
