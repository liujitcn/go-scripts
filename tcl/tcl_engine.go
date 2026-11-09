package tcl

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"modernc.org/libc"
	libtcl "modernc.org/tcl/lib"

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
	openLibs []string
	interp   *tcl.Interp

	scripts []scriptEntry

	hostFuncs map[string]any

	source      source.Reader
	initialized bool
	lastError   error
	quota       atomic.Pointer[script_engine.Quota]

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

// SetOpenLibs 设置由隔离宿主批准的原生命令；必须在 Init 前调用，不能由脚本请求自行授权。
func (e *engine) SetOpenLibs(names ...string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.openLibs = append([]string(nil), names...)
}

// Init 创建安全解释器，保留计算和宿主注册能力，禁止直接访问操作系统资源。
func (e *engine) Init(_ context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.initialized {
		return ErrTCLEngineAlreadyInitialized
	}
	var err error
	if os.Getenv("TCL_LIBRARY") == "" {
		var mountPoint string
		mountPoint, err = tcl.MountLibraryVFS()
		if err != nil {
			return fmt.Errorf("初始化 TCL 内置库失败: %w", err)
		}
		err = os.Setenv("TCL_LIBRARY", mountPoint)
		if err != nil {
			return err
		}
	}
	var interp *tcl.Interp
	interp, err = tcl.NewInterp()
	if err != nil {
		return fmt.Errorf("创建 TCL 解释器失败: %w", err)
	}
	if libtcl.XTcl_MakeSafe(interp.TLS(), interp.Handle()) != libtcl.TCL_OK {
		err = interp.Close()
		if err != nil {
			return fmt.Errorf("关闭 TCL 解释器失败: %w", err)
		}
		return fmt.Errorf("无法启用 TCL 安全解释器")
	}
	for _, name := range e.openLibs {
		var pointer uintptr
		pointer, err = libc.CString(name)
		if err != nil {
			return err
		}
		status := libtcl.XTcl_ExposeCommand(interp.TLS(), interp.Handle(), pointer, pointer)
		libc.Xfree(interp.TLS(), pointer)
		if status != libtcl.TCL_OK {
			closeErr := interp.Close()
			if closeErr != nil {
				return fmt.Errorf("释放 TCL 解释器失败: %w", closeErr)
			}
			return fmt.Errorf("TCL 宿主能力未注册: %s", name)
		}
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

// Close 释放解释器和监听资源，执行结束前不会关闭仍在运行的解释器。
func (e *engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.initialized {
		e.setLastError(ErrTCLEngineNotInitialized)
		return ErrTCLEngineNotInitialized
	}

	e.stopAllWatchers()

	e.execMu.Lock()

	err := e.interp.Close()
	e.interp = nil
	e.execMu.Unlock()

	e.scripts = nil
	e.hostFuncs = nil
	e.initialized = false

	e.lastErrorMu.Lock()
	e.lastError = nil
	e.lastErrorMu.Unlock()

	if err != nil {
		return fmt.Errorf("关闭 TCL 解释器失败: %w", err)
	}
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

// Execute 按顺序执行已加载脚本，并对每次解释器求值应用上下文取消。
func (e *engine) Execute(ctx context.Context) (any, error) {
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
		result, err := e.evaluate(ctx, s.src)
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

// ExecuteString 在安全解释器中执行源码，超时或取消会中断解释器。
func (e *engine) ExecuteString(ctx context.Context, name string, code string) (any, error) {
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

	result, err := e.evaluate(ctx, code)
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

// RegisterGlobal 使用 Tcl 对象接口传值，变量名和内容不经过源码拼接或求值。
func (e *engine) RegisterGlobal(name string, value any) error {
	if !e.IsInitialized() {
		return ErrTCLEngineNotInitialized
	}
	e.execMu.Lock()
	defer e.execMu.Unlock()
	pointer, err := libc.CString(name)
	if err != nil {
		return err
	}
	defer libc.Xfree(e.interp.TLS(), pointer)
	var object uintptr
	object, err = newTclValue(e.interp, value)
	if err != nil {
		return err
	}
	if libtcl.XTcl_SetVar2Ex(e.interp.TLS(), e.interp.Handle(), pointer, 0, object, 0) == 0 {
		return fmt.Errorf("设置 TCL 变量失败")
	}
	e.ClearError()
	return nil
}

// GetGlobal 读取变量对象而不执行由变量名拼接的脚本。
func (e *engine) GetGlobal(name string) (any, error) {
	if !e.IsInitialized() {
		return nil, ErrTCLEngineNotInitialized
	}
	e.execMu.Lock()
	defer e.execMu.Unlock()
	pointer, err := libc.CString(name)
	if err != nil {
		return nil, err
	}
	defer libc.Xfree(e.interp.TLS(), pointer)
	result := libtcl.XTcl_GetVar(e.interp.TLS(), e.interp.Handle(), pointer, 0)
	if result == 0 {
		return nil, fmt.Errorf("%w: %s", ErrTCLGlobalNotFound, name)
	}
	e.ClearError()
	return parseTCLValue(libc.GoString(result)), nil
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////

// RegisterFunction 将 Go 宿主函数注册为 Tcl 可调用命令。
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
		func(clientData any, in *tcl.Interp, args []string) int {

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

// SetQuota 设置解释器求值的时间预算。
func (e *engine) SetQuota(quota script_engine.Quota) {
	e.quota.Store(&quota)
}

// evaluate 在持有解释器执行锁时同步求值，并等待原生取消回调结束后才释放解释器。
func (e *engine) evaluate(ctx context.Context, code string) (string, error) {
	timeout := time.Duration(0)
	if quota := e.quota.Load(); quota != nil {
		timeout = quota.Timeout
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	finished := make(chan struct{})
	interpreter := e.interp.Handle()
	stop := context.AfterFunc(ctx, func() {
		tls := libc.NewTLS()
		defer tls.Close()
		libtcl.XTcl_CancelEval(tls, interpreter, 0, 0, libtcl.TCL_CANCEL_UNWIND)
		close(finished)
	})
	defer func() {
		if !stop() {
			<-finished
		}
	}()
	result, err := e.interp.Eval(code)
	if ctx.Err() != nil {
		return "", fmt.Errorf("%w: %w", script_engine.ErrQuotaExceeded, ctx.Err())
	}
	return result, err
}
