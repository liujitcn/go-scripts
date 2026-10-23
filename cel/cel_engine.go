package cel

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/google/cel-go/cel"
	script_engine "github.com/liujitcn/go-scripts"
	"github.com/liujitcn/go-scripts/source"
)

func init() {
	_ = script_engine.Register(script_engine.CELType, func() (script_engine.Engine, error) {
		return newCELEngine()
	})
}

type compiledExpr struct {
	name string
	expr string
	ast  *cel.Ast
	prg  cel.Program
}

type funcInfo struct {
	fn       any          // the Go function
	params   []*cel.Type  // parameter types
	retType  *cel.Type    // return type
	funcType reflect.Type // Go reflect.Type of fn
}

type globalInfo struct {
	value any       // the Go value
	typ   *cel.Type // CEL type
}

//

type engine struct {
	env       *cel.Env
	programs  []compiledExpr
	hostFuncs map[string]*funcInfo
	globals   map[string]*globalInfo

	source      source.Reader
	initialized bool
	lastError   error

	mu          sync.RWMutex // protects initialized, programs, source, globals, hostFuncs
	execMu      sync.Mutex   // protects env
	lastErrorMu sync.RWMutex // protects lastError

	// 热更新状态。
	watchers   map[string]context.CancelFunc
	watchersMu sync.Mutex
}

func newCELEngine() (*engine, error) {
	return &engine{
		initialized: false,
		watchers:    make(map[string]context.CancelFunc),
	}, nil
}

func (e *engine) GetType() script_engine.Type {
	return script_engine.CELType
}

func (e *engine) Init(_ context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.initialized {
		e.setLastError(ErrCELEngineAlreadyInitialized)
		return ErrCELEngineAlreadyInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()

	env, err := cel.NewEnv()
	if err != nil {
		wrapped := fmt.Errorf("cel engine: init env: %w", err)
		e.setLastError(wrapped)
		return wrapped
	}

	e.env = env
	e.hostFuncs = make(map[string]*funcInfo)
	e.globals = make(map[string]*globalInfo)
	e.initialized = true
	e.lastError = nil

	return nil
}

func (e *engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.initialized {
		e.setLastError(ErrCELEngineNotInitialized)
		return ErrCELEngineNotInitialized
	}

	e.stopAllWatchers()

	e.execMu.Lock()
	defer e.execMu.Unlock()

	e.env = nil
	e.programs = nil
	e.hostFuncs = nil
	e.globals = nil
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
		e.setLastError(ErrCELEngineNotInitialized)
		return ErrCELEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("cel engine: no source bound")
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
		e.setLastError(ErrCELEngineNotInitialized)
		return ErrCELEngineNotInitialized
	}

	e.execMu.Lock()
	if e.env == nil {
		e.execMu.Unlock()
		e.setLastError(ErrCLEEnvNotInitialized)
		return ErrCLEEnvNotInitialized
	}

	ast, prg, err := e.compileAndProgram(code)
	e.execMu.Unlock()
	if err != nil {
		wrapped := fmt.Errorf("%w: %s: %v", ErrCELCompileFailed, name, err)
		e.setLastError(wrapped)
		return wrapped
	}

	e.mu.Lock()
	e.programs = append(e.programs, compiledExpr{
		name: name,
		expr: code,
		ast:  ast,
		prg:  prg,
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
		e.setLastError(ErrCELEngineNotInitialized)
		return nil, ErrCELEngineNotInitialized
	}

	e.mu.RLock()
	if len(e.programs) == 0 {
		e.mu.RUnlock()
		e.setLastError(ErrCELNoExpressionLoaded)
		return nil, ErrCELNoExpressionLoaded
	}
	progs := make([]compiledExpr, len(e.programs))
	copy(progs, e.programs)
	vars := e.evalVars()
	e.mu.RUnlock()

	var lastResult any
	for _, p := range progs {
		result, _, err := p.prg.Eval(vars)
		if err != nil {
			wrapped := fmt.Errorf("%w: %s: %v", ErrCELEvalFailed, p.name, err)
			e.setLastError(wrapped)
			return nil, wrapped
		}
		lastResult = celValToGo(result)
	}

	e.ClearError()
	return lastResult, nil
}

func (e *engine) ExecuteFromKey(ctx context.Context, key string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrCELEngineNotInitialized)
		return nil, ErrCELEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("cel engine: no source bound")
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
		e.setLastError(ErrCELEngineNotInitialized)
		return nil, ErrCELEngineNotInitialized
	}

	e.mu.RLock()
	vars := e.evalVars()
	e.mu.RUnlock()

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.env == nil {
		e.setLastError(ErrCLEEnvNotInitialized)
		return nil, ErrCLEEnvNotInitialized
	}

	_, prg, err := e.compileAndProgram(code)
	if err != nil {
		wrapped := fmt.Errorf("%w: %s: %v", ErrCELCompileFailed, name, err)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	result, _, err := prg.Eval(vars)
	if err != nil {
		wrapped := fmt.Errorf("%w: %s: %v", ErrCELEvalFailed, name, err)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	e.ClearError()
	return celValToGo(result), nil
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

func (e *engine) RegisterGlobal(name string, value any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrCELEngineNotInitialized)
		return ErrCELEngineNotInitialized
	}

	celType := inferCelType(reflect.TypeOf(value))

	e.mu.Lock()
	e.globals[name] = &globalInfo{value: value, typ: celType}
	err := e.rebuildEnvLocked()
	e.mu.Unlock()

	if err != nil {
		e.setLastError(err)
		return err
	}

	e.ClearError()
	return nil
}

func (e *engine) GetGlobal(name string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrCELEngineNotInitialized)
		return nil, ErrCELEngineNotInitialized
	}

	e.mu.RLock()
	g, ok := e.globals[name]
	e.mu.RUnlock()
	if !ok {
		wrapped := fmt.Errorf("%w: %s", ErrCELGlobalNotFound, name)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	e.ClearError()
	return g.value, nil
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

func (e *engine) RegisterFunction(name string, fn any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrCELEngineNotInitialized)
		return ErrCELEngineNotInitialized
	}

	fnType := reflect.TypeOf(fn)
	if fnType.Kind() != reflect.Func {
		err := fmt.Errorf("cel engine: RegisterFunction expects a func type, got %T", fn)
		e.setLastError(err)
		return err
	}

	params := make([]*cel.Type, fnType.NumIn())
	for i := 0; i < fnType.NumIn(); i++ {
		params[i] = inferCelType(fnType.In(i))
	}

	var retType *cel.Type
	if fnType.NumOut() > 0 {
		retType = inferCelType(fnType.Out(0))
	} else {
		retType = cel.NullType
	}

	e.mu.Lock()
	e.hostFuncs[name] = &funcInfo{
		fn:       fn,
		params:   params,
		retType:  retType,
		funcType: fnType,
	}
	err := e.rebuildEnvLocked()
	e.mu.Unlock()

	if err != nil {
		e.setLastError(err)
		return err
	}

	e.ClearError()
	return nil
}

func (e *engine) CallFunction(_ context.Context, name string, args ...any) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrCELEngineNotInitialized)
		return nil, ErrCELEngineNotInitialized
	}

	e.mu.RLock()
	fi, ok := e.hostFuncs[name]
	e.mu.RUnlock()
	if !ok {
		wrapped := fmt.Errorf("%w: %s", ErrCELFunctionNotFound, name)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	fnVal := reflect.ValueOf(fi.fn)
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
		e.setLastError(ErrCELEngineNotInitialized)
		return ErrCELEngineNotInitialized
	}

	syms, ok := module.(map[string]any)
	if !ok {
		err := fmt.Errorf("cel engine: RegisterModule expects map[string]any, got %T", module)
		e.setLastError(err)
		return err
	}

	e.mu.Lock()
	for key, val := range syms {
		globalName := name + "_" + key
		e.globals[globalName] = &globalInfo{
			value: val,
			typ:   inferCelType(reflect.TypeOf(val)),
		}
	}
	err := e.rebuildEnvLocked()
	e.mu.Unlock()

	if err != nil {
		e.setLastError(err)
		return err
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
		e.setLastError(ErrCELEngineNotInitialized)
		return ErrCELEngineNotInitialized
	}

	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("cel engine: no source bound")
		e.setLastError(err)
		return err
	}

	watcher, ok := src.(source.Watcher)
	if !ok {
		err := fmt.Errorf("cel engine: source does not implement Watcher")
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
				script_engine.GetLogger().Warn(watchCtx, "cel engine: hot reload failed",
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
