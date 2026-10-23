package tcl

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"

	"modernc.org/tcl"

	script_engine "github.com/liujitcn/go-scripts"
	"github.com/liujitcn/go-scripts/source"
)

func init() {
	_ = script_engine.Register(script_engine.TclType, func() (script_engine.Engine, error) {
		return newTCLEngine()
	})
}

type scriptEntry struct {
	name string
	src  string
}

//

type engine struct {
	interp *tcl.Interp

	scripts []scriptEntry

	hostFuncs map[string]any

	source      source.Reader
	initialized bool
	lastError   error

	mu          sync.RWMutex // protects initialized, scripts, source, hostFuncs
	execMu      sync.Mutex   // protects interp
	lastErrorMu sync.RWMutex // protects lastError

	// 热更新状态。
	watchers   map[string]context.CancelFunc
	watchersMu sync.Mutex
}

func newTCLEngine() (*engine, error) {
	return &engine{
		initialized: false,
		watchers:    make(map[string]context.CancelFunc),
		hostFuncs:   make(map[string]any),
	}, nil
}

func (e *engine) GetType() script_engine.Type {
	return script_engine.TclType
}

func (e *engine) Init(_ context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.initialized {
		e.setLastError(ErrTCLEngineAlreadyInitialized)
		return ErrTCLEngineAlreadyInitialized
	}

	if os.Getenv("TCL_LIBRARY") == "" {
		if mountPoint, err := tcl.MountLibraryVFS(); err == nil {
			os.Setenv("TCL_LIBRARY", mountPoint)
		}
	}

	interp, err := tcl.NewInterp()
	if err != nil {
		wrapped := fmt.Errorf("tcl engine: create interpreter: %w", err)
		e.setLastError(wrapped)
		return wrapped
	}

	e.execMu.Lock()
	e.interp = interp
	e.execMu.Unlock()

	e.scripts = nil
	e.hostFuncs = make(map[string]any)
	e.initialized = true
	e.lastError = nil

	return nil
}

func (e *engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.initialized {
		e.setLastError(ErrTCLEngineNotInitialized)
		return ErrTCLEngineNotInitialized
	}

	e.stopAllWatchers()

	e.execMu.Lock()

	e.interp = nil
	e.execMu.Unlock()

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
		e.setLastError(ErrTCLEngineNotInitialized)
		return ErrTCLEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("tcl engine: no source bound")
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
		e.setLastError(ErrTCLEngineNotInitialized)
		return ErrTCLEngineNotInitialized
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
		e.setLastError(ErrTCLEngineNotInitialized)
		return nil, ErrTCLEngineNotInitialized
	}

	e.mu.RLock()
	if len(e.scripts) == 0 {
		e.mu.RUnlock()
		e.setLastError(ErrTCLNoScriptLoaded)
		return nil, ErrTCLNoScriptLoaded
	}
	scripts := make([]scriptEntry, len(e.scripts))
	copy(scripts, e.scripts)
	e.mu.RUnlock()

	e.execMu.Lock()
	defer e.execMu.Unlock()

	if e.interp == nil {
		e.setLastError(ErrTCLEngineNotInitialized)
		return nil, ErrTCLEngineNotInitialized
	}

	var lastResult string
	for _, s := range scripts {
		result, err := e.interp.Eval(s.src)
		if err != nil {
			wrapped := fmt.Errorf("%w: %s: %v", ErrTCLEvalFailed, s.name, err)
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
		e.setLastError(ErrTCLEngineNotInitialized)
		return nil, ErrTCLEngineNotInitialized
	}
	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("tcl engine: no source bound")
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
		e.setLastError(ErrTCLEngineNotInitialized)
		return nil, ErrTCLEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()

	if e.interp == nil {
		e.setLastError(ErrTCLEngineNotInitialized)
		return nil, ErrTCLEngineNotInitialized
	}

	result, err := e.interp.Eval(code)
	if err != nil {
		wrapped := fmt.Errorf("%w: %s: %v", ErrTCLEvalFailed, name, err)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	e.ClearError()
	return parseTCLValue(result), nil
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

func (e *engine) RegisterGlobal(name string, value any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrTCLEngineNotInitialized)
		return ErrTCLEngineNotInitialized
	}

	tclVal := goToTCLString(value)
	script := fmt.Sprintf("set %s %s", name, tclVal)

	e.execMu.Lock()
	defer e.execMu.Unlock()

	if e.interp == nil {
		e.setLastError(ErrTCLEngineNotInitialized)
		return ErrTCLEngineNotInitialized
	}

	_, err := e.interp.Eval(script)
	if err != nil {
		wrapped := fmt.Errorf("tcl engine: register global %q: %w", name, err)
		e.setLastError(wrapped)
		return wrapped
	}

	e.ClearError()
	return nil
}

func (e *engine) GetGlobal(name string) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrTCLEngineNotInitialized)
		return nil, ErrTCLEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()

	if e.interp == nil {
		e.setLastError(ErrTCLEngineNotInitialized)
		return nil, ErrTCLEngineNotInitialized
	}

	script := fmt.Sprintf("set %s", name)
	result, err := e.interp.Eval(script)
	if err != nil {
		wrapped := fmt.Errorf("%w: %s", ErrTCLGlobalNotFound, name)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	e.ClearError()
	return parseTCLValue(result), nil
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

func (e *engine) RegisterFunction(name string, fn any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrTCLEngineNotInitialized)
		return ErrTCLEngineNotInitialized
	}

	if reflect.TypeOf(fn).Kind() != reflect.Func {
		err := fmt.Errorf("tcl engine: RegisterFunction expects a func type, got %T", fn)
		e.setLastError(err)
		return err
	}

	e.mu.Lock()
	e.hostFuncs[name] = fn
	e.mu.Unlock()

	e.execMu.Lock()
	defer e.execMu.Unlock()

	if e.interp == nil {
		e.setLastError(ErrTCLEngineNotInitialized)
		return ErrTCLEngineNotInitialized
	}

	cmdName := sanitizeCmdName(name)
	_, err := e.interp.NewCommand(cmdName,
		func(clientData interface{}, in *tcl.Interp, args []string) int {

			goArgs := make([]any, len(args)-1)
			for i, s := range args[1:] {
				goArgs[i] = parseTCLValue(s)
			}
			result := callHostFunc(fn, goArgs)
			in.SetResult(goToTCLString(result))
			return 0 // TCL_OK
		},
		nil, // clientData
		nil, // deleteProc
	)
	if err != nil {
		wrapped := fmt.Errorf("tcl engine: register function %q: %w", name, err)
		e.setLastError(wrapped)
		return wrapped
	}

	e.ClearError()
	return nil
}

func (e *engine) CallFunction(_ context.Context, name string, args ...any) (any, error) {
	if !e.IsInitialized() {
		e.setLastError(ErrTCLEngineNotInitialized)
		return nil, ErrTCLEngineNotInitialized
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()

	if e.interp == nil {
		e.setLastError(ErrTCLEngineNotInitialized)
		return nil, ErrTCLEngineNotInitialized
	}

	e.mu.RLock()
	hostFn, isHost := e.hostFuncs[name]
	e.mu.RUnlock()
	if isHost {
		result := callHostFunc(hostFn, args)
		e.ClearError()
		return result, nil
	}

	cmdName := sanitizeCmdName(name)
	parts := []string{cmdName}
	for _, a := range args {
		parts = append(parts, goToTCLString(a))
	}
	script := strings.Join(parts, " ")

	result, err := e.interp.Eval(script)
	if err != nil {

		errStr := err.Error()
		if strings.Contains(errStr, "invalid command name") ||
			strings.Contains(errStr, "return code: 1") {
			wrapped := fmt.Errorf("%w: %s", ErrTCLFunctionNotFound, name)
			e.setLastError(wrapped)
			return nil, wrapped
		}
		wrapped := fmt.Errorf("%w: %s: %v", ErrTCLCallFailed, name, err)
		e.setLastError(wrapped)
		return nil, wrapped
	}

	e.ClearError()
	return parseTCLValue(result), nil
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

func (e *engine) RegisterModule(name string, module any) error {
	if !e.IsInitialized() {
		e.setLastError(ErrTCLEngineNotInitialized)
		return ErrTCLEngineNotInitialized
	}

	syms, ok := module.(map[string]any)
	if !ok {
		err := fmt.Errorf("tcl engine: RegisterModule expects map[string]any, got %T", module)
		e.setLastError(err)
		return err
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()

	if e.interp == nil {
		e.setLastError(ErrTCLEngineNotInitialized)
		return ErrTCLEngineNotInitialized
	}

	for key, val := range syms {
		globalName := name + "_" + key
		script := fmt.Sprintf("set %s %s", globalName, goToTCLString(val))
		_, err := e.interp.Eval(script)
		if err != nil {
			wrapped := fmt.Errorf("tcl engine: register module %q key %q: %w", name, key, err)
			e.setLastError(wrapped)
			return wrapped
		}
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
		e.setLastError(ErrTCLEngineNotInitialized)
		return ErrTCLEngineNotInitialized
	}

	e.mu.RLock()
	src := e.source
	e.mu.RUnlock()
	if src == nil {
		err := fmt.Errorf("tcl engine: no source bound")
		e.setLastError(err)
		return err
	}

	watcher, ok := src.(source.Watcher)
	if !ok {
		err := fmt.Errorf("tcl engine: source does not implement Watcher")
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
				script_engine.GetLogger().Warn(watchCtx, "tcl engine: hot reload failed",
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
