package lua

import (
	"sync"

	lua "github.com/yuin/gopher-lua"
)

const defaultMaxSaved = 10

var luaPool = newStatePool()

type luaStateArray []*lua.LState

type statePool struct {
	m        sync.Mutex
	saved    luaStateArray
	maxSaved int
	closed   bool
	options  lua.Options
}

func newStatePool() *statePool {
	return newStatePoolWithOptions(lua.Options{
		CallStackSize:       4096,
		RegistrySize:        4096,
		SkipOpenLibs:        true,
		IncludeGoStackTrace: true,
	})
}

func newStatePoolWithOptions(opts lua.Options) *statePool {
	return &statePool{
		saved:    make(luaStateArray, 0, defaultMaxSaved),
		maxSaved: defaultMaxSaved,
		options:  opts,
	}
}

func (pl *statePool) SetOptions(opts lua.Options) {
	pl.m.Lock()
	pl.options = opts
	pl.m.Unlock()
}

func (pl *statePool) createLuaState() *lua.LState {
	pl.m.Lock()
	opts := pl.options
	pl.m.Unlock()
	return pl.createLuaStateWithOptions(opts)
}

func (pl *statePool) createLuaStateWithOptions(options lua.Options) *lua.LState {
	vm := lua.NewState(options)
	return vm
}

func (pl *statePool) Borrow() *lua.LState {
	pl.m.Lock()
	n := len(pl.saved)
	if n > 0 {
		x := pl.saved[n-1]
		pl.saved = pl.saved[:n-1]
		pl.m.Unlock()
		return x
	}
	pl.m.Unlock()

	return pl.createLuaState()
}

func (pl *statePool) Return(L *lua.LState) {
	if L == nil {
		return
	}

	pl.m.Lock()
	if pl.closed {
		pl.m.Unlock()

		L.Close()
		return
	}

	if len(pl.saved) < pl.maxSaved {
		pl.saved = append(pl.saved, L)
		pl.m.Unlock()
		return
	}
	pl.m.Unlock()

	L.Close()
}

func (pl *statePool) Shutdown() {
	pl.m.Lock()
	if pl.closed {
		pl.m.Unlock()
		return
	}
	pl.closed = true
	toClose := pl.saved
	pl.saved = nil
	pl.m.Unlock()

	for _, L := range toClose {
		if L != nil {
			L.Close()
		}
	}
}
