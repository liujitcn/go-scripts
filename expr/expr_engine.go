package expr

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	script_engine "github.com/liujitcn/go-scripts"
	"github.com/liujitcn/go-scripts/source"
)

func init() {
	_ = script_engine.Register(script_engine.ExprType, func() (script_engine.Engine, error) {
		return newExprEngine()
	})
}

type compiledExpr struct {
	name    string
	expr    string
	program *vm.Program
}

//

type engine struct {
	programs []compiledExpr
	env      map[string]any // combined env: globals + functions + modules

	source      source.Reader
	initialized bool
	lastError   error

	mu          sync.RWMutex // protects initialized, programs, source, env
	execMu      sync.Mutex   // protects compilation and evaluation
	lastErrorMu sync.RWMutex // protects lastError

	// 热更新状态。
	watchers   map[string]context.CancelFunc
	watchersMu sync.Mutex
}

func newExprEngine() (*engine, error) {
	return &engine{
		initialized: false,
		watchers:    make(map[string]context.CancelFunc),
	}, nil
}

func (e *engine) GetType() script_engine.Type {
	return script_engine.ExprType
}

func (e *engine) Init(_ context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.initialized {
		e.setLastError(ErrExprEngineAlreadyInitialized)
		return ErrExprEngineAlreadyInitialized
	}

	e.env = make(map[string]any)
	e.initialized = true
	e.lastError = nil

	return nil
}

func (e *engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.initialized {
		e.setLastError(ErrExprEngineNotInitialized)
		return ErrExprEngineNotInitialized
	}

	e.stopAllWatchers()

	e.programs = nil
	e.env = nil
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
		e.setLastError(ErrExprEngineNotInitialized)
		return ErrExprEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("expr engine: no source bound")
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
		e.setLastError(ErrExprEngineNotInitialized)
		return ErrExprEngineNotInitialized
	}

	e.mu.RLock()
	env := e.snapshotEnv()
	e.mu.RUnlock()

	e.execMu.Lock()
	program, err := expr.Compile(code, expr.Env(env))
	e.execMu.Unlock()
	if err != nil {
		wrapped := fmt.Errorf("%w: %s: %v", ErrExprCompileFailed, name, err)
		e.setLastError(wrapped)
		return wrapped
	}

	e.mu.Lock()
	e.programs = append(e.programs, compiledExpr{
		name:    name,
		expr:    code,
		program: program,
	})
	e.mu.Unlock()

	e.ClearError()
	return nil
}

////////////////////////////////////////////////////////////////////////////////
// 脚本执行。
////////////////////////////////////////////////////////////////////////////////

func (e *engine) Execute(_ context.Context) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrExprEngineNotInitialized)
		return nil, ErrExprEngineNotInitialized
	}

	e.mu.RLock()
	if len(e.programs) == 0 {
		e.mu.RUnlock()
		e.setLastError(ErrExprNoExpressionLoaded)
		return nil, ErrExprNoExpressionLoaded
	}
	progs := make([]compiledExpr, len(e.programs))
	copy(progs, e.programs)
	env := e.snapshotEnv()
	e.mu.RUnlock()

	var lastResult any
	for _, p := range progs {
		result, err := vm.Run(p.program, env)
		if err != nil {
			wrapped := fmt.Errorf("%w: %s: %v", ErrExprRunFailed, p.name, err)
			e.setLastError(wrapped)
			return nil, wrapped
		}
		lastResult = result
	}

	e.ClearError()
	return lastResult, nil
}

func (e *engine) ExecuteFromKey(ctx context.Context, key string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrExprEngineNotInitialized)
		return nil, ErrExprEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("expr engine: no source bound")
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
		e.setLastError(ErrExprEngineNotInitialized)
		return nil, ErrExprEngineNotInitialized
	}

	e.mu.RLock()
	env := e.snapshotEnv()
	e.mu.RUnlock()

	e.execMu.Lock()
	defer e.execMu.Unlock()

	program, err := expr.Compile(code, expr.Env(env))
	if err != nil {
		wrapped := fmt.Errorf("%w: %s: %v", ErrExprCompileFailed, name, err)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	result, err := vm.Run(program, env)
	if err != nil {
		wrapped := fmt.Errorf("%w: %s: %v", ErrExprRunFailed, name, err)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	e.ClearError()
	return result, nil
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

func (e *engine) RegisterGlobal(name string, value any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrExprEngineNotInitialized)
		return ErrExprEngineNotInitialized
	}

	e.mu.Lock()
	e.env[name] = value
	e.mu.Unlock()

	e.ClearError()
	return nil
}

func (e *engine) GetGlobal(name string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrExprEngineNotInitialized)
		return nil, ErrExprEngineNotInitialized
	}

	e.mu.RLock()
	val, ok := e.env[name]
	e.mu.RUnlock()
	if !ok {
		wrapped := fmt.Errorf("%w: %s", ErrExprGlobalNotFound, name)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	e.ClearError()
	return val, nil
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

func (e *engine) RegisterFunction(name string, fn any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrExprEngineNotInitialized)
		return ErrExprEngineNotInitialized
	}

	if reflect.TypeOf(fn).Kind() != reflect.Func {
		err := fmt.Errorf("expr engine: RegisterFunction expects a func type, got %T", fn)
		e.setLastError(err)
		return err
	}

	e.mu.Lock()
	e.env[name] = fn
	e.mu.Unlock()

	e.ClearError()
	return nil
}

func (e *engine) CallFunction(_ context.Context, name string, args ...any) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrExprEngineNotInitialized)
		return nil, ErrExprEngineNotInitialized
	}

	e.mu.RLock()
	fn, ok := e.env[name]
	e.mu.RUnlock()
	if !ok {
		wrapped := fmt.Errorf("%w: %s", ErrExprFunctionNotFound, name)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	fnVal := reflect.ValueOf(fn)
	if fnVal.Kind() != reflect.Func {
		wrapped := fmt.Errorf("%w: %s is not a function", ErrExprFunctionNotFound, name)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	argVals := make([]reflect.Value, len(args))
	for i, a := range args {
		argVals[i] = reflect.ValueOf(a)
	}

	results := fnVal.Call(argVals)
	e.ClearError()
	if len(results) == 0 {
		return nil, nil
	}
	return results[0].Interface(), nil
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

func (e *engine) RegisterModule(name string, module any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrExprEngineNotInitialized)
		return ErrExprEngineNotInitialized
	}

	syms, ok := module.(map[string]any)
	if !ok {
		err := fmt.Errorf("expr engine: RegisterModule expects map[string]any, got %T", module)
		e.setLastError(err)
		return err
	}

	e.mu.Lock()
	e.env[name] = syms
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
		e.setLastError(ErrExprEngineNotInitialized)
		return ErrExprEngineNotInitialized
	}

	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("expr engine: no source bound")
		e.setLastError(err)
		return err
	}

	watcher, ok := src.(source.Watcher)
	if !ok {
		err := fmt.Errorf("expr engine: source does not implement Watcher")
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
				script_engine.GetLogger().Warn(watchCtx, "expr engine: hot reload failed",
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
