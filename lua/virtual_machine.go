package lua

import (
	"fmt"
	"os"
	"path/filepath"

	script_engine "github.com/liujitcn/go-scripts"

	"github.com/tengattack/gluacrypto"
	"github.com/vadv/gopher-lua-libs/http"
	"github.com/vadv/gopher-lua-libs/json"
	"github.com/yuin/gluamapper"
	lua "github.com/yuin/gopher-lua"
	luar "layeh.com/gopher-luar"
)

type TableMap map[string]interface{}

type virtualMachine struct {
	L *lua.LState
	F *lua.LFunction

	openLibs []string
}

func newVirtualMachine(openLibs []string) *virtualMachine {
	exec := &virtualMachine{
		L:        luaPool.Borrow(),
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
	AllowedLibBase = lua.BaseLibName

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

func (e *virtualMachine) initBuiltinLibs() {
	if len(e.openLibs) == 0 {
		e.L.OpenLibs()
	} else {
		e.openAllowedLibs()
	}

	json.Preload(e.L)
	http.Preload(e.L)

	gluacrypto.Preload(e.L)

	e.RegisterFunction("GetLuaPath", func(vm *lua.LState) int {

		e.L.Push(lua.LString(GetRunPath() + "/script"))
		return 1
	})
}

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

func (e *virtualMachine) Destroy() {
	if e.L != nil {
		luaPool.Return(e.L)
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

func (e *virtualMachine) CallFunction(name string, args ...interface{}) {
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

func (e *virtualMachine) PCall(f string, args ...interface{}) {
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

func (e *virtualMachine) BindStruct(name string, data interface{}) {
	e.L.SetGlobal(name, luar.New(e.L, data))
}

func (e *virtualMachine) GetLuaTableToStruct(name string, out interface{}) error {
	return gluamapper.Map(e.L.GetGlobal(name).(*lua.LTable), &out)
}

func (e *virtualMachine) doCompiledFile() error {
	e.L.Push(e.F)
	return e.L.PCall(0, lua.MultRet, nil)
}

func (e *virtualMachine) convertToLValue(val interface{}) lua.LValue {
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
	case map[string]interface{}:
		return e.convertToLTable(v)
	case []interface{}:
		lt := e.L.NewTable()
		for k, v := range v {
			lt.RawSetInt(k+1, e.convertToLValue(v))
		}
		return lt
	default:
		return nil
	}
}

func (e *virtualMachine) convertFromLValue(lv lua.LValue) interface{} {
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

			ret := make(map[string]interface{})
			v.ForEach(func(key, value lua.LValue) {
				keyStr := fmt.Sprint(e.convertFromLValue(key))
				ret[keyStr] = e.convertFromLValue(value)
			})
			return ret
		} else {

			ret := make([]interface{}, 0, maxN)
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

func (e *virtualMachine) convertToLTable(data map[string]interface{}) *lua.LTable {
	lt := e.L.NewTable()

	for k, v := range data {
		lt.RawSetString(k, e.convertToLValue(v))
	}

	return lt
}

func (e *virtualMachine) convertFromLTable(lv *lua.LTable) map[string]interface{} {
	returnData, _ := e.convertFromLValue(lv).(map[string]interface{})
	return returnData
}
