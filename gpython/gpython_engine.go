package gpython

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	_ "github.com/go-python/gpython/stdlib"

	"github.com/go-python/gpython/py"

	script_engine "github.com/liujitcn/go-scripts"
	"github.com/liujitcn/go-scripts/source"
)

func init() {
	_ = script_engine.Register(script_engine.GPythonType, func() (script_engine.Engine, error) {
		return newGPythonEngine()
	})
}

type scriptEntry struct {
	name string
	code *py.Code
}

//

type engine struct {
	ctx     py.Context    // the gpython context (interpreter instance)
	module  *py.Module    // the main module for script execution
	scripts []scriptEntry // compiled scripts queued for execution

	hostFuncs map[string]any

	runtimeHooks []script_engine.RuntimeHook

	source      source.Reader
	initialized bool
	lastError   error

	mu          sync.RWMutex // protects initialized, scripts, source, hostFuncs, runtimeHooks
	execMu      sync.Mutex   // protects ctx, module
	lastErrorMu sync.RWMutex // protects lastError

	// 热更新状态。
	watchers   map[string]context.CancelFunc
	watchersMu sync.Mutex
}

func newGPythonEngine() (*engine, error) {
	return &engine{
		initialized: false,
		watchers:    make(map[string]context.CancelFunc),
	}, nil
}

func (e *engine) GetType() script_engine.Type {
	return script_engine.GPythonType
}

func (e *engine) Init(ctx context.Context) error {

	e.mu.Lock()
	if e.initialized {
		e.setLastError(ErrGPythonEngineAlreadyInitialized)
		e.mu.Unlock()
		return ErrGPythonEngineAlreadyInitialized
	}

	e.execMu.Lock()
	e.ctx = py.NewContext(py.DefaultContextOpts())

	mainImpl := py.ModuleImpl{
		CodeSrc: "",
	}

	mod, err := e.ctx.ModuleInit(&mainImpl)
	if err != nil {
		e.execMu.Unlock()
		e.mu.Unlock()
		wrapped := fmt.Errorf("gpython engine: init module: %w", err)
		e.setLastError(wrapped)
		return wrapped
	}

	e.module = mod
	e.hostFuncs = make(map[string]any)
	e.scripts = nil
	e.initialized = true
	e.lastError = nil

	hooks := append([]script_engine.RuntimeHook(nil), e.runtimeHooks...)
	e.execMu.Unlock()
	e.mu.Unlock()

	for _, h := range hooks {
		if err := h(ctx); err != nil {
			e.setLastError(err)
			return err
		}
	}

	return nil
}

func (e *engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.initialized {
		e.setLastError(ErrGPythonEngineNotInitialized)
		return ErrGPythonEngineNotInitialized
	}

	e.stopAllWatchers()

	e.execMu.Lock()
	defer e.execMu.Unlock()

	if e.ctx != nil {
		e.ctx.Close()
		e.ctx = nil
	}
	e.module = nil
	e.scripts = nil
	e.hostFuncs = nil
	e.initialized = false

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
		e.setLastError(ErrGPythonEngineNotInitialized)
		return ErrGPythonEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("gpython engine: no source bound")
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
		e.setLastError(ErrGPythonEngineNotInitialized)
		return ErrGPythonEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.ctx == nil {
		e.setLastError(ErrGPythonContextNotInitialized)
		return ErrGPythonContextNotInitialized
	}

	compiled, err := py.Compile(code, name, py.ExecMode, 0, true)
	if err != nil {
		wrapped := fmt.Errorf("%w: %s: %v", ErrGPythonCompileFailed, name, err)
		e.setLastError(wrapped)
		return wrapped
	}

	e.mu.Lock()
	e.scripts = append(e.scripts, scriptEntry{name: name, code: compiled})
	e.mu.Unlock()

	e.ClearError()
	return nil
}

////////////////////////////////////////////////////////////////////////////////
// 脚本执行。
////////////////////////////////////////////////////////////////////////////////

func (e *engine) Execute(_ context.Context) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrGPythonEngineNotInitialized)
		return nil, ErrGPythonEngineNotInitialized
	}

	e.mu.RLock()
	if len(e.scripts) == 0 {
		e.mu.RUnlock()
		e.setLastError(ErrGPythonNoScriptLoaded)
		return nil, ErrGPythonNoScriptLoaded
	}
	scripts := make([]scriptEntry, len(e.scripts))
	copy(scripts, e.scripts)
	e.mu.RUnlock()

	e.execMu.Lock()
	defer e.execMu.Unlock()

	if e.ctx == nil || e.module == nil {
		e.setLastError(ErrGPythonContextNotInitialized)
		return nil, ErrGPythonContextNotInitialized
	}

	for _, s := range scripts {
		_, err := e.ctx.RunCode(s.code, e.module.Globals, e.module.Globals, nil)
		if err != nil {
			wrapped := fmt.Errorf("%w: %s: %v", ErrGPythonRunFailed, s.name, err)
			e.setLastError(wrapped)
			return nil, wrapped
		}
	}

	e.ClearError()
	return nil, nil
}

func (e *engine) ExecuteFromKey(ctx context.Context, key string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrGPythonEngineNotInitialized)
		return nil, ErrGPythonEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("gpython engine: no source bound")
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

func (e *engine) ExecuteString(ctx context.Context, name string, code string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrGPythonEngineNotInitialized)
		return nil, ErrGPythonEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()

	if e.ctx == nil || e.module == nil {
		e.setLastError(ErrGPythonContextNotInitialized)
		return nil, ErrGPythonContextNotInitialized
	}

	compiled, err := py.Compile(code, name, py.ExecMode, 0, true)
	if err != nil {
		wrapped := fmt.Errorf("%w: %s: %v", ErrGPythonCompileFailed, name, err)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	_, err = e.ctx.RunCode(compiled, e.module.Globals, e.module.Globals, nil)
	if err != nil {
		wrapped := fmt.Errorf("%w: %s: %v", ErrGPythonRunFailed, name, err)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	e.ClearError()
	return nil, nil
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

func (e *engine) RegisterGlobal(name string, value any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrGPythonEngineNotInitialized)
		return ErrGPythonEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()

	if e.module == nil {
		e.setLastError(ErrGPythonContextNotInitialized)
		return ErrGPythonContextNotInitialized
	}

	pyVal := goToPyObj(value)
	if pyVal == nil {
		pyVal = py.None
	}

	_, err := py.SetAttrString(e.module.Globals, name, pyVal)
	if err != nil {
		wrapped := fmt.Errorf("gpython engine: register global %q: %w", name, err)
		e.setLastError(wrapped)
		return wrapped
	}

	e.ClearError()
	return nil
}

func (e *engine) GetGlobal(name string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrGPythonEngineNotInitialized)
		return nil, ErrGPythonEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()

	if e.module == nil {
		e.setLastError(ErrGPythonContextNotInitialized)
		return nil, ErrGPythonContextNotInitialized
	}

	obj, err := py.GetAttrString(e.module.Globals, name)
	if err != nil || obj == nil {
		wrapped := fmt.Errorf("%w: %s", ErrGPythonGlobalNotFound, name)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	e.ClearError()
	return pyObjToGo(obj), nil
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

func (e *engine) RegisterFunction(name string, fn any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrGPythonEngineNotInitialized)
		return ErrGPythonEngineNotInitialized
	}

	if reflect.TypeOf(fn).Kind() != reflect.Func {
		err := fmt.Errorf("gpython engine: RegisterFunction expects a func type, got %T", fn)
		e.setLastError(err)
		return err
	}

	e.mu.Lock()
	e.hostFuncs[name] = fn
	e.mu.Unlock()

	e.execMu.Lock()
	defer e.execMu.Unlock()

	if e.module == nil {
		e.setLastError(ErrGPythonContextNotInitialized)
		return ErrGPythonContextNotInitialized
	}

	wrapped := createPythonCallable(name, fn)
	_, err := py.SetAttrString(e.module.Globals, name, wrapped)
	if err != nil {
		wrappedErr := fmt.Errorf("gpython engine: register function %q: %w", name, err)
		e.setLastError(wrappedErr)
		return wrappedErr
	}

	e.ClearError()
	return nil
}

func (e *engine) CallFunction(_ context.Context, name string, args ...any) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrGPythonEngineNotInitialized)
		return nil, ErrGPythonEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()

	if e.module == nil {
		e.setLastError(ErrGPythonContextNotInitialized)
		return nil, ErrGPythonContextNotInitialized
	}

	obj, err := py.GetAttrString(e.module.Globals, name)
	if err != nil || obj == nil {
		wrapped := fmt.Errorf("%w: %s", ErrGPythonFunctionNotFound, name)
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

	callCode := buildPythonCallCode(name, len(args))
	compiled, compileErr := py.Compile(callCode, "call_"+name, py.EvalMode, 0, true)
	if compileErr != nil {
		wrapped := fmt.Errorf("%w: %s is not callable: %v", ErrGPythonFunctionNotFound, name, compileErr)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	for i, a := range args {
		argName := fmt.Sprintf("__arg%d__", i)
		_, _ = py.SetAttrString(e.module.Globals, argName, goToPyObj(a))
	}

	result, runErr := e.ctx.RunCode(compiled, e.module.Globals, e.module.Globals, nil)
	if runErr != nil {
		wrapped := fmt.Errorf("gpython engine: call %q failed: %w", name, runErr)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	e.ClearError()
	return pyObjToGo(result), nil
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

func (e *engine) RegisterModule(name string, module any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrGPythonEngineNotInitialized)
		return ErrGPythonEngineNotInitialized
	}

	syms, ok := module.(map[string]any)
	if !ok {
		err := fmt.Errorf("gpython engine: RegisterModule expects map[string]any, got %T", module)
		e.setLastError(err)
		return err
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()

	if e.module == nil {
		e.setLastError(ErrGPythonContextNotInitialized)
		return ErrGPythonContextNotInitialized
	}

	for key, val := range syms {
		globalName := name + "_" + key
		pyVal := goToPyObj(val)
		if pyVal == nil {
			pyVal = py.None
		}
		if _, err := py.SetAttrString(e.module.Globals, globalName, pyVal); err != nil {
			wrapped := fmt.Errorf("gpython engine: register module %q key %q: %w", name, key, err)
			e.setLastError(wrapped)
			return wrapped
		}
	}

	e.ClearError()
	return nil
}

//

func (e *engine) AddRuntimeHook(hook script_engine.RuntimeHook) error {
	if hook == nil {
		return nil
	}

	e.mu.Lock()
	e.runtimeHooks = append(e.runtimeHooks, hook)
	initialized := e.initialized
	e.mu.Unlock()

	if !initialized {

		return nil
	}

	if err := hook(context.Background()); err != nil {
		e.setLastError(err)
		return err
	}
	return nil
}

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
		e.setLastError(ErrGPythonEngineNotInitialized)
		return ErrGPythonEngineNotInitialized
	}

	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("gpython engine: no source bound")
		e.setLastError(err)
		return err
	}

	watcher, ok := src.(source.Watcher)
	if !ok {
		err := fmt.Errorf("gpython engine: source does not implement Watcher")
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
				script_engine.GetLogger().Warn(watchCtx, "gpython engine: hot reload failed",
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
