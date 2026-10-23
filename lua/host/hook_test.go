package host

//

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
)

type fakeRegisterCall struct {
	name        string
	description string
}

type fakeAddScriptCall struct {
	hookName string
	script   Script
}

type fakeCallbackCall struct {
	hookName string
	L        *lua.LState
	fn       *lua.LFunction
}

type fakeHookEngine struct {
	registerCalls  []fakeRegisterCall
	addScriptCalls []fakeAddScriptCall
	callbackCalls  []fakeCallbackCall
	listResult     []string
	registerErr    error
	addScriptErr   error
}

func (f *fakeHookEngine) RegisterHook(name, description string) error {
	f.registerCalls = append(f.registerCalls, fakeRegisterCall{name, description})
	return f.registerErr
}

func (f *fakeHookEngine) AddScript(hookName string, script interface{}) error {
	if s, ok := script.(Script); ok {
		f.addScriptCalls = append(f.addScriptCalls, fakeAddScriptCall{hookName, s})
	}
	return f.addScriptErr
}

func (f *fakeHookEngine) ListHooks() []string { return f.listResult }

func (f *fakeHookEngine) RegisterCallback(hookName string, L *lua.LState, fn *lua.LFunction) {
	f.callbackCalls = append(f.callbackCalls, fakeCallbackCall{hookName, L, fn})
}

func newHookLuaState(engine HookEngine) *lua.LState {
	L := lua.NewState()
	RegisterHookAPI(L, engine, nil)
	return L
}

func TestRegisterHookAPI_ModuleLoads(t *testing.T) {
	L := newHookLuaState(&fakeHookEngine{})
	defer L.Close()

	err := L.DoString(`
		local h = require "kratos_hook"
		assert(type(h) == "table", "module should be a table")
		assert(h.register ~= nil, "register should exist")
		assert(h.add_script ~= nil, "add_script should exist")
		assert(h.list ~= nil, "list should exist")
	`)
	require.NoError(t, err)
}

func TestHookAPI_NilEngineYieldsEmptyModule(t *testing.T) {
	L := newHookLuaState(nil)
	defer L.Close()

	err := L.DoString(`
		local h = require "kratos_hook"
		assert(type(h) == "table", "module should still be a table")
		assert(h.register == nil, "empty module should have no register")
		assert(h.add_script == nil, "empty module should have no add_script")
		assert(h.list == nil, "empty module should have no list")
	`)
	require.NoError(t, err)
}

func TestHookAPI_RegisterWithCallback(t *testing.T) {
	engine := &fakeHookEngine{}
	L := newHookLuaState(engine)
	defer L.Close()

	err := L.DoString(`
		local h = require "kratos_hook"
		local ok = h.register("myhook", "hook-desc", function() end)
		assert(ok == true, "register should return true")
	`)
	require.NoError(t, err)

	require.Len(t, engine.registerCalls, 1)
	require.Equal(t, "myhook", engine.registerCalls[0].name)
	require.Equal(t, "hook-desc", engine.registerCalls[0].description)
	require.Len(t, engine.callbackCalls, 1)
	require.Equal(t, "myhook", engine.callbackCalls[0].hookName)
	require.NotNil(t, engine.callbackCalls[0].L)
	require.NotNil(t, engine.callbackCalls[0].fn)
}

func TestHookAPI_RegisterWithoutCallback(t *testing.T) {
	engine := &fakeHookEngine{}
	L := newHookLuaState(engine)
	defer L.Close()

	err := L.DoString(`
		local h = require "kratos_hook"
		local ok = h.register("plainhook")
		assert(ok == true, "register should return true")
	`)
	require.NoError(t, err)

	require.Len(t, engine.registerCalls, 1)
	require.Equal(t, "plainhook", engine.registerCalls[0].name)
	require.Equal(t, "", engine.registerCalls[0].description)
	require.Empty(t, engine.callbackCalls)
}

func TestHookAPI_RegisterWithNonFunctionCallback(t *testing.T) {
	engine := &fakeHookEngine{}
	L := newHookLuaState(engine)
	defer L.Close()

	err := L.DoString(`
		local h = require "kratos_hook"
		local ok = h.register("h3", "d3", 42)
		assert(ok == true, "register should still return true")
	`)
	require.NoError(t, err)

	require.Len(t, engine.registerCalls, 1)
	require.Equal(t, "h3", engine.registerCalls[0].name)
	require.Empty(t, engine.callbackCalls)
}

func TestHookAPI_RegisterEngineErrorStillRegistersCallback(t *testing.T) {
	engine := &fakeHookEngine{registerErr: errors.New("hook register failed")}
	L := newHookLuaState(engine)
	defer L.Close()

	err := L.DoString(`
		local h = require "kratos_hook"
		local ok = h.register("boom", "bd", function() end)
		assert(ok == true, "register should return true despite engine error")
	`)
	require.NoError(t, err)

	require.Len(t, engine.registerCalls, 1)
	require.Len(t, engine.callbackCalls, 1)
	require.Equal(t, "boom", engine.callbackCalls[0].hookName)
}

func TestHookAPI_AddScript_FullFields(t *testing.T) {
	engine := &fakeHookEngine{}
	L := newHookLuaState(engine)
	defer L.Close()

	err := L.DoString(`
		local h = require "kratos_hook"
		local ok = h.add_script("targethook", {
			name = "s1",
			source = "print(1)",
			enabled = false,
			priority = 7,
			description = "d1"
		})
		assert(ok == true, "add_script should return true")
	`)
	require.NoError(t, err)

	require.Len(t, engine.addScriptCalls, 1)
	call := engine.addScriptCalls[0]
	require.Equal(t, "targethook", call.hookName)
	require.Equal(t, "targethook", call.script.Hook)
	require.Equal(t, "s1", call.script.Name)
	require.Equal(t, "print(1)", call.script.Source)
	require.False(t, call.script.Enabled)
	require.Equal(t, 7, call.script.Priority)
	require.Equal(t, "d1", call.script.Description)
}

func TestHookAPI_AddScript_DefaultFields(t *testing.T) {
	engine := &fakeHookEngine{}
	L := newHookLuaState(engine)
	defer L.Close()

	err := L.DoString(`
		local h = require "kratos_hook"
		local ok = h.add_script("t2", {name = "s2", source = "x"})
		assert(ok == true, "add_script should return true")
	`)
	require.NoError(t, err)

	require.Len(t, engine.addScriptCalls, 1)
	call := engine.addScriptCalls[0]
	require.Equal(t, "t2", call.hookName)
	require.Equal(t, "t2", call.script.Hook)
	require.Equal(t, "s2", call.script.Name)
	require.Equal(t, "x", call.script.Source)
	require.True(t, call.script.Enabled)
	require.Equal(t, 0, call.script.Priority)
	require.Equal(t, "", call.script.Description)
}

// 保持默认值 0。
func TestHookAPI_AddScript_PriorityNotNumber(t *testing.T) {
	engine := &fakeHookEngine{}
	L := newHookLuaState(engine)
	defer L.Close()

	err := L.DoString(`
		local h = require "kratos_hook"
		local ok = h.add_script("t3", {name = "s3", source = "x", priority = "high"})
		assert(ok == true, "add_script should return true")
	`)
	require.NoError(t, err)

	require.Len(t, engine.addScriptCalls, 1)
	require.Equal(t, 0, engine.addScriptCalls[0].script.Priority)
}

func TestHookAPI_AddScript_EngineError(t *testing.T) {
	engine := &fakeHookEngine{addScriptErr: errors.New("nope")}
	L := newHookLuaState(engine)
	defer L.Close()

	err := L.DoString(`
		local h = require "kratos_hook"
		local ok, e = h.add_script("t4", {name = "s4", source = "x"})
		assert(ok == false, "add_script should return false on engine error")
		assert(type(e) == "string", "error should be surfaced as string")
	`)
	require.NoError(t, err)

	require.Len(t, engine.addScriptCalls, 1)
	require.Equal(t, "t4", engine.addScriptCalls[0].hookName)
}

func TestHookAPI_List(t *testing.T) {
	engine := &fakeHookEngine{listResult: []string{"a", "b"}}
	L := newHookLuaState(engine)
	defer L.Close()

	err := L.DoString(`
		local h = require "kratos_hook"
		local l = h.list()
		assert(#l == 2, "list length should be 2, got " .. tostring(#l))
		assert(l[1] == "a", "first entry should be a")
		assert(l[2] == "b", "second entry should be b")
	`)
	require.NoError(t, err)
}
