package js

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dop251/goja"

	script_engine "github.com/liujitcn/go-scripts"
	"github.com/liujitcn/go-scripts/source"
)

func init() {
	_ = script_engine.Register(script_engine.JavaScriptType, func() (script_engine.Engine, error) {
		return newJavascriptEngine()
	})
}

//

type engine struct {
	runtime  *goja.Runtime   // the JavaScript runtime
	programs []*goja.Program // compiled programs queued for execution

	runtimeHooks []script_engine.RuntimeHook

	source      source.Reader // optional script source (File / S3 / Mem / ...)
	initialized bool
	lastError   error

	mu          sync.RWMutex // protects initialized, programs, source and runtimeHooks
	execMu      sync.Mutex   // protects runtime
	lastErrorMu sync.RWMutex // protects lastError

	quota *script_engine.Quota

	// 热更新状态。
	watchers   map[string]context.CancelFunc // key -> cancel func for the watch goroutine
	watchersMu sync.Mutex                    // protects watchers
}

func newJavascriptEngine() (*engine, error) {
	return &engine{
		initialized: false,
		watchers:    make(map[string]context.CancelFunc),
	}, nil
}

func (e *engine) GetType() script_engine.Type {
	return script_engine.JavaScriptType
}

func (e *engine) Init(ctx context.Context) error {
	newRt := goja.New()

	e.mu.Lock()
	if e.initialized {
		e.setLastError(ErrJavascriptEngineAlreadyInitialized)
		e.mu.Unlock()
		return ErrJavascriptEngineAlreadyInitialized
	}

	e.execMu.Lock()
	e.runtime = newRt
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
		e.setLastError(ErrJavascriptEngineNotInitialized)
		return ErrJavascriptEngineNotInitialized
	}

	// 停止所有活跃监听。
	e.stopAllWatchers()

	e.execMu.Lock()
	defer e.execMu.Unlock()

	e.initialized = false
	e.runtime = nil
	e.programs = nil

	e.lastErrorMu.Lock()
	e.lastError = nil
	e.lastErrorMu.Unlock()

	return nil
}

func (e *engine) ClearPrograms() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.programs = nil
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
		e.setLastError(ErrJavascriptEngineNotInitialized)
		return ErrJavascriptEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("javascript engine: no source bound")
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
		e.setLastError(ErrJavascriptEngineNotInitialized)
		return ErrJavascriptEngineNotInitialized
	}

	program, err := goja.Compile(name, code, true)
	if err != nil {
		e.setLastError(err)
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.initialized {
		e.setLastError(ErrJavascriptEngineNotInitialized)
		return ErrJavascriptEngineNotInitialized
	}
	e.programs = append(e.programs, program)

	e.ClearError()
	return nil
}

////////////////////////////////////////////////////////////////////////////////
// 脚本执行。
////////////////////////////////////////////////////////////////////////////////

func (e *engine) Execute(ctx context.Context) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrJavascriptEngineNotInitialized)
		return nil, ErrJavascriptEngineNotInitialized
	}

	e.mu.RLock()
	progs := make([]*goja.Program, len(e.programs))
	copy(progs, e.programs)
	e.mu.RUnlock()

	if len(progs) == 0 {
		e.setLastError(ErrJavascriptNoProgramLoaded)
		return nil, ErrJavascriptNoProgramLoaded
	}

	results := make([]any, 0, len(progs))
	for _, p := range progs {
		res, err := e.RunProgram(ctx, p)
		if err != nil {

			return nil, err
		}
		results = append(results, res)
	}

	e.ClearError()
	return results, nil
}

func (e *engine) ExecuteFromKey(ctx context.Context, key string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrJavascriptEngineNotInitialized)
		return nil, ErrJavascriptEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("javascript engine: no source bound")
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
		e.setLastError(ErrJavascriptEngineNotInitialized)
		return nil, ErrJavascriptEngineNotInitialized
	}

	program, err := goja.Compile(name, code, true)
	if err != nil {
		e.setLastError(err)
		return nil, err
	}

	defer e.armCancelWatch(ctx)()

	result, err := e.withRuntime(ctx, func(rt *goja.Runtime) (any, error) {
		var retErr error
		defer func() {
			if r := recover(); r != nil {
				retErr = fmt.Errorf("panic in ExecuteString: %v", r)
			}
		}()

		val, runErr := rt.RunProgram(program)
		if runErr != nil || val == nil {
			return nil, runErr
		}
		return val.Export(), retErr
	})

	if err != nil {
		e.setLastError(err)
		return nil, err
	}
	e.ClearError()
	return result, nil
}

////////////////////////////////////////////////////////////////////////////////
// 全局变量、函数和模块。
////////////////////////////////////////////////////////////////////////////////

func (e *engine) RegisterGlobal(name string, value any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrJavascriptEngineNotInitialized)
		return ErrJavascriptEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.runtime == nil {
		e.setLastError(ErrJavascriptRuntimeNotInitialized)
		return ErrJavascriptRuntimeNotInitialized
	}
	_ = e.runtime.Set(name, value)

	e.ClearError()

	return nil
}

func (e *engine) GetGlobal(name string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrJavascriptEngineNotInitialized)
		return nil, ErrJavascriptEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.runtime == nil {
		e.setLastError(ErrJavascriptRuntimeNotInitialized)
		return nil, ErrJavascriptRuntimeNotInitialized
	}
	val := e.runtime.Get(name)
	if val == nil {
		err := fmt.Errorf("global variable %s not found", name)
		e.setLastError(err)
		return nil, err
	}
	result := val.Export()

	e.ClearError()

	return result, nil
}

func (e *engine) RegisterFunction(name string, fn any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrJavascriptEngineNotInitialized)
		return ErrJavascriptEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.runtime == nil {
		e.setLastError(ErrJavascriptRuntimeNotInitialized)
		return ErrJavascriptRuntimeNotInitialized
	}

	_ = e.runtime.Set(name, fn)

	e.ClearError()

	return nil
}

func (e *engine) CallFunction(ctx context.Context, name string, args ...any) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrJavascriptEngineNotInitialized)
		return nil, ErrJavascriptEngineNotInitialized
	}

	defer e.armCancelWatch(ctx)()

	result, err := e.withRuntime(ctx, func(rt *goja.Runtime) (any, error) {
		var (
			res    any
			retErr error
		)
		defer func() {
			if r := recover(); r != nil {
				retErr = fmt.Errorf("panic in CallFunction %s: %v", name, r)
			}
		}()

		v := rt.Get(name)
		if v == nil {
			return nil, fmt.Errorf("function %s not found", name)
		}
		fn, ok := goja.AssertFunction(v)
		if !ok {
			return nil, fmt.Errorf("%s is not a function", name)
		}

		vals := make([]goja.Value, len(args))
		for i, a := range args {
			vals[i] = rt.ToValue(a)
		}

		callRes, callErr := fn(goja.Undefined(), vals...)
		if callErr != nil {
			return nil, callErr
		}
		if callRes == nil {
			return nil, nil
		}
		res = callRes.Export()
		return res, retErr
	})

	if err != nil {
		e.setLastError(err)
		return nil, err
	}
	e.ClearError()
	return result, nil
}

func (e *engine) RegisterModule(name string, module any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrJavascriptEngineNotInitialized)
		return ErrJavascriptEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.runtime == nil {
		e.setLastError(ErrJavascriptRuntimeNotInitialized)
		return ErrJavascriptRuntimeNotInitialized
	}

	moduleObj := e.runtime.NewObject()
	if m, ok := module.(map[string]any); ok {
		for k, v := range m {
			_ = moduleObj.Set(k, v)
		}
		_ = e.runtime.Set(name, moduleObj)
	} else {
		_ = e.runtime.Set(name, module)
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

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

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
		e.setLastError(ErrJavascriptEngineNotInitialized)
		return nil, ErrJavascriptEngineNotInitialized
	}

	e.mu.RLock()
	progs := make([]*goja.Program, len(e.programs))
	copy(progs, e.programs)
	quota := e.quota
	e.mu.RUnlock()

	if len(progs) == 0 {
		e.setLastError(ErrJavascriptNoProgramLoaded)
		return nil, ErrJavascriptNoProgramLoaded
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.runtime == nil {
		e.setLastError(ErrJavascriptRuntimeNotInitialized)
		return nil, ErrJavascriptRuntimeNotInitialized
	}

	if err := ctx.Err(); err != nil {
		e.setLastError(err)
		return nil, err
	}
	e.runtime.ClearInterrupt()

	var stopWatch chan struct{}
	if quota != nil && quota.Timeout > 0 {
		stopWatch = make(chan struct{})
		rt := e.runtime
		go e.armQuotaWatch(ctx, quota.Timeout, rt, stopWatch)
	}

	var results []any
	for _, p := range progs {
		val, err := e.runtime.RunProgram(p)
		if err != nil {
			if stopWatch != nil {
				close(stopWatch)
			}
			if isJSQuotaError(err) {
				wrapped := fmt.Errorf("%w: %v", script_engine.ErrQuotaExceeded, err)
				e.setLastError(wrapped)
				return nil, wrapped
			}
			e.setLastError(err)
			return nil, err
		}
		if val != nil {
			results = append(results, val.Export())
		}
	}
	if stopWatch != nil {
		close(stopWatch)
	}
	e.ClearError()
	return results, nil
}

func (e *engine) armQuotaWatch(ctx context.Context, timeout time.Duration, rt *goja.Runtime, stop <-chan struct{}) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-timer.C:
		if rt != nil {
			rt.Interrupt(script_engine.ErrQuotaExceeded)
		}
	case <-ctx.Done():
		if rt != nil {
			rt.Interrupt(ctx.Err())
		}
	case <-stop:
	}
}

func isJSQuotaError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, script_engine.ErrQuotaExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	msg := err.Error()
	return msg == script_engine.ErrQuotaExceeded.Error() ||
		msg == context.DeadlineExceeded.Error()
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

//

func (e *engine) armCancelWatch(ctx context.Context) func() {
	e.execMu.Lock()
	rt := e.runtime
	e.execMu.Unlock()

	deadline, hasDeadline := ctx.Deadline()
	var timer *time.Timer
	if hasDeadline {
		timer = time.NewTimer(time.Until(deadline))
	}
	done := make(chan struct{})

	go func() {
		var interrupt error
		select {
		case <-done:
			return
		default:
		}
		if timer != nil {
			select {
			case <-timer.C:
				interrupt = context.DeadlineExceeded
			case <-done:
				return
			}
		} else {
			select {
			case <-ctx.Done():
				interrupt = ctx.Err()
			case <-done:
				return
			}
		}

		select {
		case <-done:
			return
		default:
		}
		if rt != nil {
			rt.Interrupt(interrupt)
		}
	}()

	return func() {
		if timer != nil {
			timer.Stop()
		}
		close(done)
	}
}

func (e *engine) withRuntime(ctx context.Context, fn func(rt *goja.Runtime) (any, error)) (any, error) {
	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.runtime == nil {
		return nil, ErrJavascriptRuntimeNotInitialized
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	e.runtime.ClearInterrupt()
	return fn(e.runtime)
}

func (e *engine) RunProgram(ctx context.Context, program *goja.Program) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrJavascriptEngineNotInitialized)
		return nil, ErrJavascriptEngineNotInitialized
	}

	defer e.armCancelWatch(ctx)()

	result, err := e.withRuntime(ctx, func(rt *goja.Runtime) (any, error) {
		val, err := rt.RunProgram(program)
		if err != nil || val == nil {
			return nil, err
		}
		return val.Export(), nil
	})

	if err != nil {
		e.setLastError(err)
		return nil, err
	}

	e.ClearError()

	return result, nil
}

////////////////////////////////////////////////////////////////////////////////
// 热更新（监听）。
////////////////////////////////////////////////////////////////////////////////

func (e *engine) StartWatch(ctx context.Context, key string) error {
	if !e.IsInitialized() {
		e.setLastError(ErrJavascriptEngineNotInitialized)
		return ErrJavascriptEngineNotInitialized
	}

	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("javascript engine: no source bound")
		e.setLastError(err)
		return err
	}

	watcher, ok := src.(source.Watcher)
	if !ok {
		err := fmt.Errorf("javascript engine: source does not implement Watcher")
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

				script_engine.GetLogger().Warn(watchCtx, "javascript engine: hot reload failed",
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
