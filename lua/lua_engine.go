package lua

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	script_engine "github.com/liujitcn/go-scripts"
	"github.com/liujitcn/go-scripts/source"
	lua "github.com/yuin/gopher-lua"
)

func init() {
	_ = script_engine.Register(script_engine.LuaType, func() (script_engine.Engine, error) {
		return newLuaEngine()
	})
}

type engine struct {
	vm          *virtualMachine
	initialized bool
	lastError   error

	source      source.Reader // optional script source (File / S3 / Mem / ...)
	mu          sync.RWMutex  // protects vm, initialized and source
	lastErrorMu sync.Mutex    // protects lastError

	runtimeHooks []script_engine.RuntimeHook

	businessGlobals map[string]struct{}

	openLibs []string

	quota *script_engine.Quota

	// 热更新状态。
	watchers   map[string]context.CancelFunc // key -> cancel func for the watch goroutine
	watchersMu sync.Mutex                    // protects watchers
}

func newLuaEngine() (*engine, error) {
	return &engine{
		initialized: false,
		watchers:    make(map[string]context.CancelFunc),
	}, nil
}

func (e *engine) GetType() script_engine.Type {
	return script_engine.LuaType
}

//

//

//
//

// )
func (e *engine) SetOpenLibs(libs ...string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.openLibs = libs
}

func (e *engine) Init(ctx context.Context) error {

	e.mu.Lock()
	if e.initialized {
		e.setLastError(ErrLuaEngineAlreadyInitialized)
		e.mu.Unlock()
		return ErrLuaEngineAlreadyInitialized
	}

	e.vm = newVirtualMachine(e.openLibs)
	e.businessGlobals = make(map[string]struct{})
	e.initialized = true
	e.ClearError()

	hooks := append([]script_engine.RuntimeHook(nil), e.runtimeHooks...)
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
		e.setLastError(ErrLuaEngineNotInitialized)
		return ErrLuaEngineNotInitialized
	}

	// 停止所有活跃监听。
	e.stopAllWatchers()

	e.vm.ClearGlobals(e.snapshotBusinessGlobals())

	e.vm.Destroy()
	e.vm = nil
	e.businessGlobals = nil
	e.initialized = false

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

func (e *engine) SetSource(source source.Reader) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.source = source
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
		e.setLastError(ErrLuaEngineNotInitialized)
		return ErrLuaEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("lua engine: no source bound")
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

//

func (e *engine) LoadMulti(ctx context.Context, keys []string) error {
	for _, k := range keys {
		if err := e.Load(ctx, k); err != nil {
			return err
		}
	}
	return nil
}

func (e *engine) LoadString(_ context.Context, _ string, source string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.initialized {
		e.setLastError(ErrLuaEngineNotInitialized)
		return ErrLuaEngineNotInitialized
	}

	if err := e.vm.LoadString(source); err != nil {
		e.setLastError(err)
		return err
	}

	e.ClearError()

	return nil
}

////////////////////////////////////////////////////////////////////////////////
// 脚本执行。
////////////////////////////////////////////////////////////////////////////////

//

func (e *engine) Execute(ctx context.Context) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrLuaEngineNotInitialized)
		return nil, ErrLuaEngineNotInitialized
	}

	done := make(chan error, 1)

	go func() {
		e.mu.Lock()
		defer e.mu.Unlock()

		if !e.initialized {
			done <- ErrLuaEngineNotInitialized
			return
		}

		if ctx.Done() != nil {
			L := e.vm.L
			L.SetContext(ctx)
			defer L.RemoveContext()
		}

		done <- e.vm.Execute()
	}()

	select {
	case <-ctx.Done():

		<-done
		e.setLastError(ctx.Err())
		return nil, ctx.Err()

	case err := <-done:
		if err != nil {
			e.setLastError(err)
			return nil, err
		}
		e.ClearError()
		return nil, nil
	}
}

func (e *engine) ExecuteFromKey(ctx context.Context, key string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrLuaEngineNotInitialized)
		return nil, ErrLuaEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("lua engine: no source bound")
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

func (e *engine) ExecuteString(ctx context.Context, _ string, source string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrLuaEngineNotInitialized)
		return nil, ErrLuaEngineNotInitialized
	}

	done := make(chan error, 1)

	go func() {
		e.mu.Lock()
		defer e.mu.Unlock()

		if !e.initialized {
			done <- ErrLuaEngineNotInitialized
			return
		}

		if ctx.Done() != nil {
			L := e.vm.L
			L.SetContext(ctx)
			defer L.RemoveContext()
		}

		done <- e.vm.ExecuteString(source)
	}()

	select {
	case <-ctx.Done():

		<-done
		e.setLastError(ctx.Err())
		return nil, ctx.Err()

	case err := <-done:
		if err != nil {
			e.setLastError(err)
			return nil, err
		}
		e.ClearError()
		return nil, nil
	}
}

////////////////////////////////////////////////////////////////////////////////
// 全局变量、函数和模块。
////////////////////////////////////////////////////////////////////////////////

func (e *engine) RegisterGlobal(name string, value any) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.initialized {
		e.setLastError(ErrLuaEngineNotInitialized)
		return ErrLuaEngineNotInitialized
	}

	e.vm.BindStruct(name, value)
	e.recordBusinessGlobal(name)

	e.ClearError()
	return nil
}

func (e *engine) GetGlobal(name string) (any, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.initialized {
		e.setLastError(ErrLuaEngineNotInitialized)
		return nil, ErrLuaEngineNotInitialized
	}

	lv := e.vm.L.GetGlobal(name)
	result := e.vm.convertFromLValue(lv)
	e.ClearError()
	return result, nil
}

func (e *engine) RegisterFunction(name string, fn any) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.initialized {
		e.setLastError(ErrLuaEngineNotInitialized)
		return ErrLuaEngineNotInitialized
	}

	if lf, ok := fn.(lua.LGFunction); ok {
		e.vm.RegisterFunction(name, lf)
		e.recordBusinessGlobal(name)
		e.ClearError()
		return nil
	}

	err := fmt.Errorf("function must be of type Lua.LGFunction")
	e.setLastError(err)
	return err
}

func (e *engine) CallFunction(ctx context.Context, name string, args ...any) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrLuaEngineNotInitialized)
		return nil, ErrLuaEngineNotInitialized
	}

	type result struct {
		value any
		err   error
	}

	done := make(chan result, 1)

	go func() {
		e.mu.Lock()
		defer e.mu.Unlock()

		if !e.initialized {
			done <- result{nil, ErrLuaEngineNotInitialized}
			return
		}

		if ctx.Done() != nil {
			L := e.vm.L
			L.SetContext(ctx)
			defer L.RemoveContext()
		}

		var lArgs []lua.LValue
		for _, arg := range args {
			lArgs = append(lArgs, e.vm.convertToLValue(arg))
		}

		err := e.vm.L.CallByParam(lua.P{
			Fn:      e.vm.L.GetGlobal(name),
			NRet:    1,
			Protect: true,
		}, lArgs...)

		if err != nil {
			done <- result{nil, err}
			return
		}

		ret := e.vm.L.Get(-1)
		e.vm.L.Pop(1)

		done <- result{e.vm.convertFromLValue(ret), nil}
	}()

	select {
	case <-ctx.Done():

		<-done
		e.setLastError(ctx.Err())
		return nil, ctx.Err()

	case res := <-done:
		if res.err != nil {
			e.setLastError(res.err)
			return res.value, res.err
		}
		e.ClearError()
		return res.value, res.err
	}
}

func (e *engine) RegisterModule(name string, module any) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.initialized {
		e.setLastError(ErrLuaEngineNotInitialized)
		return ErrLuaEngineNotInitialized
	}

	if mod, ok := module.(lua.LGFunction); ok {
		e.vm.RegisterModule(name, mod)
		e.recordBusinessGlobal(name)
		e.ClearError()
		return nil
	}

	err := fmt.Errorf("module must be of type Lua.LGFunction")
	e.setLastError(err)
	return err
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

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

func (e *engine) recordBusinessGlobal(name string) {
	if e.businessGlobals == nil {
		e.businessGlobals = make(map[string]struct{})
	}
	e.businessGlobals[name] = struct{}{}
}

func (e *engine) snapshotBusinessGlobals() []string {
	if len(e.businessGlobals) == 0 {
		return nil
	}
	names := make([]string, 0, len(e.businessGlobals))
	for name := range e.businessGlobals {
		names = append(names, name)
	}
	return names
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

//

//

func (e *engine) SetQuota(q script_engine.Quota) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if q.Timeout == 0 && q.MaxInstructions == 0 {
		e.quota = nil
	} else {
		cp := q
		e.quota = &cp
	}
}

//

//

func (e *engine) ExecuteSync(ctx context.Context) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrLuaEngineNotInitialized)
		return nil, ErrLuaEngineNotInitialized
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.initialized || e.vm == nil {
		e.setLastError(ErrLuaEngineNotInitialized)
		return nil, ErrLuaEngineNotInitialized
	}

	L := e.vm.L

	runCtx, cancel := e.composeRunCtx(ctx)
	if runCtx != nil {
		L.SetContext(runCtx)
		defer func() {
			cancel()
			L.RemoveContext()
		}()
	} else {
		cancel()
	}

	if err := e.vm.Execute(); err != nil {
		if isQuotaError(err) {
			wrapped := fmt.Errorf("%w: %v", script_engine.ErrQuotaExceeded, err)
			e.setLastError(wrapped)
			return nil, wrapped
		}
		e.setLastError(err)
		return nil, err
	}
	e.ClearError()
	return nil, nil
}

func (e *engine) composeRunCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	cancel := func() {}
	if e.quota == nil || e.quota.Timeout == 0 {
		return nil, cancel
	}
	quotaCtx, qCancel := context.WithTimeout(context.Background(), e.quota.Timeout)
	if ctx == nil {
		return quotaCtx, qCancel
	}

	merged, mCancel := context.WithTimeout(ctx, e.quota.Timeout)
	qCancel()
	return merged, mCancel
}

func isQuotaError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, script_engine.ErrQuotaExceeded) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "context deadline exceeded")
}

////////////////////////////////////////////////////////////////////////////////
// 错误处理。
////////////////////////////////////////////////////////////////////////////////

func (e *engine) GetLastError() error {
	e.lastErrorMu.Lock()
	defer e.lastErrorMu.Unlock()

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
		e.setLastError(ErrLuaEngineNotInitialized)
		return ErrLuaEngineNotInitialized
	}

	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("lua engine: no source bound")
		e.setLastError(err)
		return err
	}

	watcher, ok := src.(source.Watcher)
	if !ok {
		err := fmt.Errorf("lua engine: source does not implement Watcher")
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

				script_engine.GetLogger().Warn(watchCtx, "lua engine: hot reload failed",
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
