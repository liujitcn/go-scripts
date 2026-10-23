package wazero

import (
	"context"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	script_engine "github.com/liujitcn/go-scripts"
	"github.com/liujitcn/go-scripts/source"
)

const hostModuleName = "host"

func init() {
	_ = script_engine.Register(script_engine.WazeroType, func() (script_engine.Engine, error) {
		return newWazeroEngine()
	})
}

//

type engine struct {
	runtime      wazero.Runtime          // the wazero runtime
	compiledMods []wazero.CompiledModule // compiled WASM modules
	instance     api.Module              // last instantiated module

	hostFunctions map[string]any
	hostModule    api.Module

	source      source.Reader
	initialized bool
	lastError   error

	mu          sync.RWMutex // protects initialized, compiledMods, source and hostFunctions
	execMu      sync.Mutex   // protects runtime and instance
	lastErrorMu sync.RWMutex // protects lastError

	// 热更新状态。
	watchers   map[string]context.CancelFunc
	watchersMu sync.Mutex
}

func newWazeroEngine() (*engine, error) {
	return &engine{
		initialized: false,
		watchers:    make(map[string]context.CancelFunc),
	}, nil
}

func (e *engine) GetType() script_engine.Type {
	return script_engine.WazeroType
}

func (e *engine) Init(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.initialized {
		e.setLastError(ErrWazeroEngineAlreadyInitialized)
		return ErrWazeroEngineAlreadyInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()

	e.runtime = wazero.NewRuntime(ctx)
	e.hostFunctions = make(map[string]any)
	e.initialized = true
	e.lastError = nil

	return nil
}

func (e *engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.initialized {
		e.setLastError(ErrWazeroEngineNotInitialized)
		return ErrWazeroEngineNotInitialized
	}

	// 停止所有活跃监听。
	e.stopAllWatchers()

	e.execMu.Lock()
	defer e.execMu.Unlock()

	// 关闭已实例化模块。
	if e.instance != nil {
		_ = e.instance.Close(context.Background())
		e.instance = nil
	}

	// 关闭已编译模块。
	for _, cm := range e.compiledMods {
		_ = cm.Close(context.Background())
	}
	e.compiledMods = nil

	// 关闭宿主模块。
	if e.hostModule != nil {
		_ = e.hostModule.Close(context.Background())
		e.hostModule = nil
	}
	e.hostFunctions = nil

	// 关闭运行时。
	if e.runtime != nil {
		_ = e.runtime.Close(context.Background())
		e.runtime = nil
	}

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
		e.setLastError(ErrWazeroEngineNotInitialized)
		return ErrWazeroEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("wazero engine: no source bound")
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

func (e *engine) LoadString(ctx context.Context, _ string, code string) error {
	if !e.IsInitialized() {
		e.setLastError(ErrWazeroEngineNotInitialized)
		return ErrWazeroEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.runtime == nil {
		e.setLastError(ErrWazeroRuntimeNotInitialized)
		return ErrWazeroRuntimeNotInitialized
	}

	compiled, err := e.runtime.CompileModule(ctx, []byte(code))
	if err != nil {
		wrapped := fmt.Errorf("%w: %v", ErrWazeroCompileFailed, err)
		e.setLastError(wrapped)
		return wrapped
	}

	e.mu.Lock()
	e.compiledMods = append(e.compiledMods, compiled)
	e.mu.Unlock()

	e.ClearError()
	return nil
}

////////////////////////////////////////////////////////////////////////////////
// 脚本执行。
////////////////////////////////////////////////////////////////////////////////

func (e *engine) Execute(ctx context.Context) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrWazeroEngineNotInitialized)
		return nil, ErrWazeroEngineNotInitialized
	}

	e.mu.RLock()
	if len(e.compiledMods) == 0 {
		e.mu.RUnlock()
		e.setLastError(ErrWazeroNoModuleLoaded)
		return nil, ErrWazeroNoModuleLoaded
	}
	last := e.compiledMods[len(e.compiledMods)-1]
	e.mu.RUnlock()

	return e.instantiateAndRun(ctx, last)
}

func (e *engine) ExecuteFromKey(ctx context.Context, key string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrWazeroEngineNotInitialized)
		return nil, ErrWazeroEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("wazero engine: no source bound")
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
		e.setLastError(ErrWazeroEngineNotInitialized)
		return nil, ErrWazeroEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.runtime == nil {
		e.setLastError(ErrWazeroRuntimeNotInitialized)
		return nil, ErrWazeroRuntimeNotInitialized
	}

	compiled, err := e.runtime.CompileModule(ctx, []byte(code))
	if err != nil {
		wrapped := fmt.Errorf("%w: %v", ErrWazeroCompileFailed, err)
		e.setLastError(wrapped)
		return nil, wrapped
	}
	defer compiled.Close(context.Background())

	return e.instantiateAndRunLocked(ctx, compiled)
}

////////////////////////////////////////////////////////////////////////////////
// 全局变量、函数和模块。
////////////////////////////////////////////////////////////////////////////////

func (e *engine) RegisterGlobal(name string, value any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrWazeroEngineNotInitialized)
		return ErrWazeroEngineNotInitialized
	}

	e.ClearError()
	return nil
}

func (e *engine) GetGlobal(name string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrWazeroEngineNotInitialized)
		return nil, ErrWazeroEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.instance == nil {
		e.setLastError(ErrWazeroNotInstantiated)
		return nil, ErrWazeroNotInstantiated
	}

	g := e.instance.ExportedGlobal(name)
	if g == nil {
		err := fmt.Errorf("wazero engine: global %q not found", name)
		e.setLastError(err)
		return nil, err
	}

	e.ClearError()
	return g.Get(), nil
}

func (e *engine) RegisterFunction(name string, fn any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrWazeroEngineNotInitialized)
		return ErrWazeroEngineNotInitialized
	}

	e.mu.Lock()
	e.hostFunctions[name] = fn
	err := e.rebuildHostModule()
	e.mu.Unlock()

	if err != nil {
		e.setLastError(err)
		return err
	}

	e.ClearError()
	return nil
}

func (e *engine) CallFunction(ctx context.Context, name string, args ...any) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrWazeroEngineNotInitialized)
		return nil, ErrWazeroEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.instance == nil {
		e.setLastError(ErrWazeroNotInstantiated)
		return nil, ErrWazeroNotInstantiated
	}

	fn := e.instance.ExportedFunction(name)
	if fn == nil {
		wrapped := fmt.Errorf("%w: %s", ErrWazeroFunctionNotFound, name)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	uintArgs := make([]uint64, len(args))
	for i, a := range args {
		uintArgs[i] = toUint64(a)
	}

	results, err := fn.Call(ctx, uintArgs...)
	if err != nil {
		wrapped := fmt.Errorf("wazero engine: call %q failed: %w", name, err)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	e.ClearError()
	if len(results) == 0 {
		return nil, nil
	}
	return results[0], nil
}

func (e *engine) RegisterModule(name string, module any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrWazeroEngineNotInitialized)
		return ErrWazeroEngineNotInitialized
	}

	syms, ok := module.(map[string]any)
	if !ok {
		err := fmt.Errorf("wazero engine: RegisterModule expects map[string]any, got %T", module)
		e.setLastError(err)
		return err
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.runtime == nil {
		e.setLastError(ErrWazeroRuntimeNotInitialized)
		return ErrWazeroRuntimeNotInitialized
	}

	builder := e.runtime.NewHostModuleBuilder(name)
	for fnName, fn := range syms {
		builder.NewFunctionBuilder().WithFunc(fn).Export(fnName)
	}
	if _, err := builder.Instantiate(context.Background()); err != nil {
		wrapped := fmt.Errorf("wazero engine: register module %q: %w", name, err)
		e.setLastError(wrapped)
		return wrapped
	}

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
		e.setLastError(ErrWazeroEngineNotInitialized)
		return ErrWazeroEngineNotInitialized
	}

	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("wazero engine: no source bound")
		e.setLastError(err)
		return err
	}

	watcher, ok := src.(source.Watcher)
	if !ok {
		err := fmt.Errorf("wazero engine: source does not implement Watcher")
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
				script_engine.GetLogger().Warn(watchCtx, "wazero engine: hot reload failed",
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
