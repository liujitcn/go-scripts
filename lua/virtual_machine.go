package lua

import (
	"fmt"
	nethttp "net/http"
	"os"
	"path/filepath"

	script_engine "github.com/liujitcn/go-scripts"

	"github.com/tengattack/gluacrypto"
	"github.com/vadv/gopher-lua-libs/http"
	httpclient "github.com/vadv/gopher-lua-libs/http/client"
	"github.com/vadv/gopher-lua-libs/json"
	"github.com/yuin/gluamapper"
	lua "github.com/yuin/gopher-lua"
	luar "layeh.com/gopher-luar"
)

// TableMap 保存 Lua 表的字符串键值映射。
type TableMap map[string]any

type virtualMachine struct {
	L *lua.LState
	F *lua.LFunction

	openLibs   []string
	httpClient *nethttp.Client
}

// newVirtualMachine 为每个执行实例创建干净状态，禁止从共享 VM 继承变量或高权限库。
func newVirtualMachine(openLibs []string) *virtualMachine {
	exec := &virtualMachine{
		L:        luaPool.createLuaState(),
		openLibs: openLibs,
	}
	exec.init()
	return exec
}

func GetRunPath() string {
	if workingDir, err := os.Getwd(); err == nil {
		if _, err = os.Stat(filepath.Join(workingDir, "script")); err == nil {
			return workingDir
		}
	}
	path, _ := filepath.Abs(filepath.Dir(os.Args[0]))
	return path
}

const (
	// AllowedLibBase 允许使用 Lua 基础计算库。
	AllowedLibBase = lua.BaseLibName
	// AllowedLibHTTP 显式允许网络扩展，仅供具有受控网络环境的宿主使用。
	AllowedLibHTTP = "http"
	// AllowedLibCrypto 显式允许加密扩展。
	AllowedLibCrypto = "crypto"

	AllowedLibLoad      = lua.LoadLibName
	AllowedLibTab       = lua.TabLibName
	AllowedLibIo        = lua.IoLibName
	AllowedLibOs        = lua.OsLibName
	AllowedLibStr       = lua.StringLibName
	AllowedLibMath      = lua.MathLibName
	AllowedLibDebug     = lua.DebugLibName
	AllowedLibChannel   = lua.ChannelLibName
	AllowedLibCoroutine = lua.CoroutineLibName
)

var stdLibOpeners = map[string]lua.LGFunction{
	lua.BaseLibName:      lua.OpenBase,
	lua.LoadLibName:      lua.OpenPackage,
	lua.TabLibName:       lua.OpenTable,
	lua.IoLibName:        lua.OpenIo,
	lua.OsLibName:        lua.OpenOs,
	lua.StringLibName:    lua.OpenString,
	lua.MathLibName:      lua.OpenMath,
	lua.DebugLibName:     lua.OpenDebug,
	lua.ChannelLibName:   lua.OpenChannel,
	lua.CoroutineLibName: lua.OpenCoroutine,
}

func (e *virtualMachine) init() {
	e.initBuiltinLibs()
}

//

//

// initBuiltinLibs 默认开放计算、JSON 和时间能力，文件与网络能力需要宿主显式授权。
func (e *virtualMachine) initBuiltinLibs() {
	if len(e.openLibs) == 0 {
		e.openLibs = []string{AllowedLibBase, AllowedLibTab, AllowedLibStr, AllowedLibMath, AllowedLibCoroutine}
	}
	// require 保留，但默认只允许预加载模块，不能从磁盘查找并执行源码。
	e.L.Push(e.L.NewFunction(lua.OpenPackage))
	e.L.Push(lua.LString(lua.LoadLibName))
	e.L.Call(1, 0)
	e.openAllowedLibs()
	allowed := make(map[string]bool, len(e.openLibs))
	for _, name := range e.openLibs {
		allowed[name] = true
	}
	json.Preload(e.L)
	if allowed[AllowedLibHTTP] {
		http.Preload(e.L)
		e.L.PreloadModule("http", e.loadHTTP)
		e.L.PreloadModule("http_client", e.loadHTTPClient)
	}
	if allowed[AllowedLibCrypto] {
		gluacrypto.Preload(e.L)
	}
	if !allowed[AllowedLibLoad] {
		packageTable := e.L.GetGlobal("package").(*lua.LTable)
		originalLoaders := e.L.GetField(packageTable, "loaders").(*lua.LTable)
		loaders := e.L.NewTable()
		loaders.RawSetInt(1, originalLoaders.RawGetInt(1))
		e.L.SetField(packageTable, "loaders", loaders)
		e.L.SetField(e.L.Get(lua.RegistryIndex), "_LOADERS", loaders)
		e.L.SetField(packageTable, "path", lua.LString(""))
		e.L.SetField(packageTable, "cpath", lua.LString(""))
		e.L.SetField(packageTable, "loadlib", lua.LNil)
		e.L.SetGlobal("dofile", lua.LNil)
		e.L.SetGlobal("loadfile", lua.LNil)
	}
	if !allowed[AllowedLibOs] {
		e.L.Push(e.L.NewFunction(lua.OpenOs))
		e.L.Push(lua.LString(lua.OsLibName))
		e.L.Call(1, 0)
		original := e.L.GetGlobal("os").(*lua.LTable)
		safeTime := e.L.NewTable()
		for _, name := range []string{"clock", "date", "difftime", "time"} {
			safeTime.RawSetString(name, original.RawGetString(name))
		}
		e.L.SetGlobal("os", safeTime)
	}
	if allowed[AllowedLibLoad] {
		e.RegisterFunction("GetLuaPath", func(vm *lua.LState) int {
			vm.Push(lua.LString(GetRunPath() + "/script"))
			return 1
		})
	}
}

// loadHTTP 保留标准 http 模块，仅替换客户端工厂的出站传输。
func (e *virtualMachine) loadHTTP(state *lua.LState) int {
	count := http.Loader(state)
	module := state.Get(-1).(*lua.LTable)
	module.RawSetString("client", state.NewFunction(e.newHTTPClient))
	return count
}

// loadHTTPClient 为直接加载 http_client 的调用应用同一受控传输。
func (e *virtualMachine) loadHTTPClient(state *lua.LState) int {
	count := httpclient.Loader(state)
	module := state.Get(-1).(*lua.LTable)
	module.RawSetString("client", state.NewFunction(e.newHTTPClient))
	return count
}

// newHTTPClient 注入宿主代理和拨号器，同时保留脚本的证书、Cookie、超时和请求头配置。
func (e *virtualMachine) newHTTPClient(state *lua.LState) int {
	count := httpclient.New(state)
	if e.httpClient == nil {
		return count
	}
	client := state.Get(-1).(*lua.LUserData).Value.(*httpclient.LuaClient)
	if approved, ok := e.httpClient.Transport.(*nethttp.Transport); ok {
		transport := client.Transport.(*nethttp.Transport).Clone()
		transport.Proxy = approved.Proxy
		transport.DialContext = approved.DialContext
		transport.DialTLSContext = nil
		client.Transport = transport
	} else {
		client.Transport = e.httpClient.Transport
	}
	client.CheckRedirect = e.httpClient.CheckRedirect
	return count
}

// openAllowedLibs 只注册宿主声明的标准库，扩展库由独立加载器处理。
func (e *virtualMachine) openAllowedLibs() {
	allowed := make(map[string]struct{}, len(e.openLibs))
	for _, name := range e.openLibs {
		allowed[name] = struct{}{}
	}

	open := func(name string) {
		opener, ok := stdLibOpeners[name]
		if !ok {
			return
		}
		e.L.Push(e.L.NewFunction(opener))
		e.L.Push(lua.LString(name))
		e.L.Call(1, 0)
	}

	if _, ok := allowed[lua.LoadLibName]; ok {
		open(lua.LoadLibName)
	}
	if _, ok := allowed[lua.BaseLibName]; ok {
		open(lua.BaseLibName)
	}
	for name := range stdLibOpeners {
		if name == lua.LoadLibName || name == lua.BaseLibName {
			continue
		}
		if _, ok := allowed[name]; ok {
			open(name)
		}
	}

	for name := range stdLibOpeners {
		if name == lua.BaseLibName || name == lua.LoadLibName {

			continue
		}
		if _, ok := allowed[name]; !ok {
			e.L.SetGlobal(name, lua.LNil)
		}
	}
}

func (e *virtualMachine) ClearGlobals(names []string) {
	if e.L == nil {
		return
	}
	for _, name := range names {
		e.L.SetGlobal(name, lua.LNil)
	}
}

// Destroy 关闭执行实例，不将修改后的全局表或库状态放回共享池。
func (e *virtualMachine) Destroy() {
	if e.L != nil {
		e.L.Close()
	}
}

func (e *virtualMachine) LoadString(source string) error {
	var lFunc *lua.LFunction
	var err error
	if lFunc, err = e.L.LoadString(source); err != nil {
		return err
	}

	e.F = lFunc

	return nil
}

func (e *virtualMachine) LoadFile(filePath string) error {
	var lFunc *lua.LFunction
	var err error
	if lFunc, err = e.L.LoadFile(filePath); err != nil {
		return err
	}

	e.F = lFunc

	return nil
}

func (e *virtualMachine) Execute() error {
	if err := e.doCompiledFile(); err != nil {
		return err
	}
	return nil
}

func (e *virtualMachine) ExecuteString(source string) error {
	if err := e.L.DoString(source); err != nil {
		return err
	}
	return nil
}

func (e *virtualMachine) ExecuteFile(filePath string) error {
	if err := e.L.DoFile(filePath); err != nil {
		return err
	}
	return nil
}

// CallFunction 转换 Go 参数并调用指定的 Lua 全局函数。
func (e *virtualMachine) CallFunction(name string, args ...any) {
	var lArgs []lua.LValue
	for _, arg := range args {
		lArgs = append(lArgs, e.convertToLValue(arg))
	}

	if err := e.L.CallByParam(lua.P{
		Fn:      e.L.GetGlobal(name),
		NRet:    1,    // number of return values
		Protect: true, // return error instead of panic on failure
	}, lArgs...); err != nil { // input arguments
		panic(err)
	}
}

// PCall 转换 Go 参数并保护调用 Lua 函数，失败时记录日志。
func (e *virtualMachine) PCall(f string, args ...any) {
	e.L.Push(e.L.GetGlobal(f))
	for _, arg := range args {
		val := e.convertToLValue(arg)
		e.L.Push(val)
	}
	if err := e.L.PCall(len(args), -1, nil); err != nil {
		script_engine.GetLogger().Error(nil, "lua pcall error", "func", f, "err", err)
	}
}

func (e *virtualMachine) PCall2(f string, args ...lua.LValue) {
	e.L.Push(e.L.GetGlobal(f))
	for _, arg := range args {
		e.L.Push(arg)
	}
	if err := e.L.PCall(len(args), -1, nil); err != nil {
		script_engine.GetLogger().Error(nil, "lua pcall2 error", "func", f, "err", err)
	}
}

func (e *virtualMachine) PCall3(f lua.LValue, args ...lua.LValue) {
	e.L.Push(f)
	for _, arg := range args {
		e.L.Push(arg)
	}
	if err := e.L.PCall(len(args), -1, nil); err != nil {
		script_engine.GetLogger().Error(nil, "lua pcall3 error", "err", err)
	}
}

func (e *virtualMachine) RegisterFunction(name string, fn lua.LGFunction) {
	e.L.SetGlobal(name, e.L.NewFunction(fn))
}

func (e *virtualMachine) RegisterModule(name string, mod lua.LGFunction) {
	e.L.Push(e.L.NewFunction(mod))
	e.L.Push(lua.LString(name))
	e.L.Call(1, 0)
}

// BindStruct 将 Go 结构体绑定为 Lua 全局变量。
func (e *virtualMachine) BindStruct(name string, data any) {
	e.L.SetGlobal(name, luar.New(e.L, data))
}

// GetLuaTableToStruct 将指定的 Lua 全局表映射到 Go 结构体。
func (e *virtualMachine) GetLuaTableToStruct(name string, out any) error {
	return gluamapper.Map(e.L.GetGlobal(name).(*lua.LTable), &out)
}

func (e *virtualMachine) doCompiledFile() error {
	e.L.Push(e.F)
	return e.L.PCall(0, lua.MultRet, nil)
}

// convertToLValue 将受支持的 Go 值转换为 Lua 值。
func (e *virtualMachine) convertToLValue(val any) lua.LValue {
	if val == nil {
		return lua.LNil
	}
	switch v := val.(type) {
	case lua.LValue:
		return v
	case bool:
		return lua.LBool(v)
	case float32:
		return lua.LNumber(v)
	case float64:
		return lua.LNumber(v)
	case int:
		return lua.LNumber(v)
	case int8:
		return lua.LNumber(v)
	case int16:
		return lua.LNumber(v)
	case int32:
		return lua.LNumber(v)
	case int64:
		return lua.LNumber(v)
	case uint8:
		return lua.LNumber(v)
	case uint16:
		return lua.LNumber(v)
	case uint32:
		return lua.LNumber(v)
	case uint64:
		return lua.LNumber(v)
	case string:
		return lua.LString(v)
	case []byte:
		ud := e.L.NewUserData()
		ud.Value = v
		return ud
	case map[string]any:
		return e.convertToLTable(v)
	case []any:
		lt := e.L.NewTable()
		for k, v := range v {
			lt.RawSetInt(k+1, e.convertToLValue(v))
		}
		return lt
	default:
		return nil
	}
}

// convertFromLValue 将 Lua 值转换为 Go 值并区分整数与浮点数。
func (e *virtualMachine) convertFromLValue(lv lua.LValue) any {
	switch v := lv.(type) {
	case *lua.LNilType:
		return nil
	case *lua.LUserData:
		return v.Value
	case lua.LBool:
		return bool(v)
	case lua.LString:
		return string(v)
	case lua.LNumber:
		f64i := float64(v)
		I64i := int64(v)
		if f64i == float64(I64i) {
			return I64i
		}
		return f64i
	case *lua.LTable:
		maxN := v.MaxN()
		if maxN == 0 {

			ret := make(map[string]any)
			v.ForEach(func(key, value lua.LValue) {
				keyStr := fmt.Sprint(e.convertFromLValue(key))
				ret[keyStr] = e.convertFromLValue(value)
			})
			return ret
		} else {

			ret := make([]any, 0, maxN)
			for i := 1; i <= maxN; i++ {
				ret = append(ret, e.convertFromLValue(v.RawGetInt(i)))
			}
			return ret
		}
	default:
		script_engine.GetLogger().Error(nil, "unsupported lua type", "type", fmt.Sprintf("%T", lv), "value", lv)
		return nil
	}
}

// convertToLTable 将 Go 映射递归转换为 Lua 表。
func (e *virtualMachine) convertToLTable(data map[string]any) *lua.LTable {
	lt := e.L.NewTable()

	for k, v := range data {
		lt.RawSetString(k, e.convertToLValue(v))
	}

	return lt
}

// convertFromLTable 将 Lua 表转换为字符串键的 Go 映射。
func (e *virtualMachine) convertFromLTable(lv *lua.LTable) map[string]any {
	returnData, _ := e.convertFromLValue(lv).(map[string]any)
	return returnData
}
