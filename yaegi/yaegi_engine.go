package yaegi

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/traefik/yaegi/interp"

	script_engine "github.com/liujitcn/go-scripts"
	"github.com/liujitcn/go-scripts/source"
)

func init() {
	_ = script_engine.Register(script_engine.YaegiType, func() (script_engine.Engine, error) {
		return newYaegiEngine()
	})
}

//

type engine struct {
	interp  *interp.Interpreter // the Yaegi interpreter
	scripts []string            // source code strings queued for execution

	runtimeHooks []script_engine.RuntimeHook

	source      source.Reader // optional script source (File / S3 / Mem / ...)
	initialized bool
	lastError   error

	mu          sync.RWMutex // protects initialized, scripts and source
	execMu      sync.Mutex   // protects interp
	lastErrorMu sync.RWMutex // protects lastError

	// 热更新状态。
	watchers   map[string]context.CancelFunc // key -> cancel func for the watch goroutine
	watchersMu sync.Mutex                    // protects watchers
}

func newYaegiEngine() (*engine, error) {
	return &engine{
		initialized: false,
		watchers:    make(map[string]context.CancelFunc),
	}, nil
}

func (e *engine) GetType() script_engine.Type {
	return script_engine.YaegiType
}

func (e *engine) Init(ctx context.Context) error {

	e.mu.Lock()
	if e.initialized {
		e.setLastError(ErrYaegiEngineAlreadyInitialized)
		e.mu.Unlock()
		return ErrYaegiEngineAlreadyInitialized
	}

	e.execMu.Lock()
	e.interp = interp.New(interp.Options{})
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
		e.setLastError(ErrYaegiEngineNotInitialized)
		return ErrYaegiEngineNotInitialized
	}

	// 停止所有活跃监听。
	e.stopAllWatchers()

	e.execMu.Lock()
	defer e.execMu.Unlock()

	e.initialized = false
	e.interp = nil
	e.scripts = nil

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
		e.setLastError(ErrYaegiEngineNotInitialized)
		return ErrYaegiEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("yaegi engine: no source bound")
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

func (e *engine) LoadString(_ context.Context, _ string, code string) error {
	if !e.IsInitialized() {
		e.setLastError(ErrYaegiEngineNotInitialized)
		return ErrYaegiEngineNotInitialized
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.initialized {
		e.setLastError(ErrYaegiEngineNotInitialized)
		return ErrYaegiEngineNotInitialized
	}
	e.scripts = append(e.scripts, code)

	e.ClearError()
	return nil
}

////////////////////////////////////////////////////////////////////////////////
// 脚本执行。
////////////////////////////////////////////////////////////////////////////////

func (e *engine) Execute(ctx context.Context) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrYaegiEngineNotInitialized)
		return nil, ErrYaegiEngineNotInitialized
	}

	e.mu.RLock()
	scripts := make([]string, len(e.scripts))
	copy(scripts, e.scripts)
	e.mu.RUnlock()

	if len(scripts) == 0 {
		e.setLastError(ErrYaegiNoScriptLoaded)
		return nil, ErrYaegiNoScriptLoaded
	}

	var lastResult any
	for _, src := range scripts {
		res, err := e.eval(ctx, src)
		if err != nil {
			return nil, err
		}
		lastResult = res
	}

	e.ClearError()
	return lastResult, nil
}

func (e *engine) ExecuteFromKey(ctx context.Context, key string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrYaegiEngineNotInitialized)
		return nil, ErrYaegiEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("yaegi engine: no source bound")
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

func (e *engine) ExecuteString(ctx context.Context, _ string, code string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrYaegiEngineNotInitialized)
		return nil, ErrYaegiEngineNotInitialized
	}
	return e.eval(ctx, code)
}

////////////////////////////////////////////////////////////////////////////////
// 全局变量、函数和模块。
////////////////////////////////////////////////////////////////////////////////

const hostPkg = "host"

func (e *engine) RegisterGlobal(name string, value any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrYaegiEngineNotInitialized)
		return ErrYaegiEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.interp == nil {
		e.setLastError(ErrYaegiInterpreterNotInitialized)
		return ErrYaegiInterpreterNotInitialized
	}

	e.interp.Use(interp.Exports{
		hostPkg: map[string]reflect.Value{
			name: reflect.ValueOf(value),
		},
	})
	// 使宿主包可以在当前解释器上下文中导入。
	_, _ = e.interp.Eval(`import "` + hostPkg + `"`)

	e.ClearError()
	return nil
}

func (e *engine) GetGlobal(name string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrYaegiEngineNotInitialized)
		return nil, ErrYaegiEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.interp == nil {
		e.setLastError(ErrYaegiInterpreterNotInitialized)
		return nil, ErrYaegiInterpreterNotInitialized
	}

	v, err := e.interp.Eval(name)
	if err != nil {

		v, err = e.interp.Eval(hostPkg + "." + name)
		if err != nil {
			wrapped := fmt.Errorf("%w: get global %q: %v", ErrYaegiEvalFailed, name, err)
			e.setLastError(wrapped)
			return nil, wrapped
		}
	}

	e.ClearError()
	return v.Interface(), nil
}

func (e *engine) RegisterFunction(name string, fn any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrYaegiEngineNotInitialized)
		return ErrYaegiEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.interp == nil {
		e.setLastError(ErrYaegiInterpreterNotInitialized)
		return ErrYaegiInterpreterNotInitialized
	}

	if reflect.TypeOf(fn).Kind() != reflect.Func {
		err := fmt.Errorf("yaegi engine: RegisterFunction expects a func type, got %T", fn)
		e.setLastError(err)
		return err
	}

	e.interp.Use(interp.Exports{
		hostPkg: map[string]reflect.Value{
			name: reflect.ValueOf(fn),
		},
	})
	// 使宿主包可以在当前解释器上下文中导入。
	_, _ = e.interp.Eval(`import "` + hostPkg + `"`)

	e.ClearError()
	return nil
}

func (e *engine) CallFunction(ctx context.Context, name string, args ...any) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrYaegiEngineNotInitialized)
		return nil, ErrYaegiEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.interp == nil {
		e.setLastError(ErrYaegiInterpreterNotInitialized)
		return nil, ErrYaegiInterpreterNotInitialized
	}

	var fnVal reflect.Value
	var evalErr error

	if strings.Contains(name, ".") {

		parts := strings.SplitN(name, ".", 2)
		_, _ = e.interp.Eval(`import "` + parts[0] + `"`)
		fnVal, evalErr = e.interp.Eval(name)
	} else {

		fnVal, evalErr = e.interp.Eval(name)
		if evalErr != nil {

			fnVal, evalErr = e.interp.Eval(hostPkg + "." + name)
		}
	}

	if evalErr != nil {
		wrapped := fmt.Errorf("%w: %v", ErrYaegiFunctionNotFound, evalErr)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	if !fnVal.IsValid() || fnVal.Kind() != reflect.Func {
		err := fmt.Errorf("%w: %s is not a function", ErrYaegiFunctionNotFound, name)
		e.setLastError(err)
		return nil, err
	}

	argVals := make([]reflect.Value, len(args))
	for i, a := range args {
		argVals[i] = reflect.ValueOf(a)
	}

	// 调用函数。
	results := fnVal.Call(argVals)

	if len(results) == 0 {
		e.ClearError()
		return nil, nil
	}

	e.ClearError()
	return results[0].Interface(), nil
}

func (e *engine) RegisterModule(name string, module any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrYaegiEngineNotInitialized)
		return ErrYaegiEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.interp == nil {
		e.setLastError(ErrYaegiInterpreterNotInitialized)
		return ErrYaegiInterpreterNotInitialized
	}

	syms, ok := module.(map[string]any)
	if !ok {
		err := fmt.Errorf("yaegi engine: RegisterModule expects map[string]any, got %T", module)
		e.setLastError(err)
		return err
	}

	pkg := make(map[string]reflect.Value, len(syms))
	for k, v := range syms {
		pkg[k] = reflect.ValueOf(v)
	}

	e.interp.Use(interp.Exports{
		name: pkg,
	})

	_, _ = e.interp.Eval(`import "` + name + `"`)

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
		e.setLastError(ErrYaegiEngineNotInitialized)
		return ErrYaegiEngineNotInitialized
	}

	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("yaegi engine: no source bound")
		e.setLastError(err)
		return err
	}

	watcher, ok := src.(source.Watcher)
	if !ok {
		err := fmt.Errorf("yaegi engine: source does not implement Watcher")
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

				script_engine.GetLogger().Warn(watchCtx, "yaegi engine: hot reload failed",
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
