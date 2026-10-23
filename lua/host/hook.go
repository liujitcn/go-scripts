package host

import (
	"context"

	lua "github.com/yuin/gopher-lua"

	"github.com/liujitcn/go-scripts/hostmodule"
)

type HookEngine interface {
	RegisterHook(name, description string) error
	AddScript(hookName string, script interface{}) error
	ListHooks() []string
	RegisterCallback(hookName string, L *lua.LState, fn *lua.LFunction)
}

type Script struct {
	ID          string
	Name        string
	Hook        string
	Source      string
	Enabled     bool
	Priority    int
	Description string
	Version     string
	Author      string
	Critical    bool
}

func RegisterHookAPI(L *lua.LState, engine HookEngine, logger hostmodule.Logger) {

	L.PreloadModule("kratos_hook", LoaderHook(engine, logger))
}

func LoaderHook(engine HookEngine, logger hostmodule.Logger) lua.LGFunction {
	return func(L *lua.LState) int {
		if engine == nil {
			L.Push(L.NewTable())
			return 1
		}

		hookModule := L.NewTable()

		hookModule.RawSetString("register", L.NewFunction(func(L *lua.LState) int {
			hookName := L.CheckString(1)
			description := L.OptString(2, "")
			callback := L.Get(3) // Optional callback function

			err := engine.RegisterHook(hookName, description)
			if err != nil {

				hostmodule.Logf(logger, context.Background(), "debug", "hook.register: %v (continuing to register callback)", err)
			} else {
				hostmodule.Logf(logger, context.Background(), "debug", "Registered hook: %s", hookName)
			}

			if callback != lua.LNil {
				if fn, ok := callback.(*lua.LFunction); ok {

					engine.RegisterCallback(hookName, L, fn)
					hostmodule.Logf(logger, context.Background(), "debug", "Registered callback for hook: %s", hookName)
				} else {
					hostmodule.Logf(logger, context.Background(), "warn", "Third argument is not a function, ignoring callback")
				}
			}

			L.Push(lua.LBool(true))
			return 1
		}))

		hookModule.RawSetString("add_script", L.NewFunction(func(L *lua.LState) int {
			hookName := L.CheckString(1)
			scriptTable := L.CheckTable(2)

			name := scriptTable.RawGetString("name").String()
			source := scriptTable.RawGetString("source").String()

			enabled := true
			if enabledVal := scriptTable.RawGetString("enabled"); enabledVal != lua.LNil {
				enabled = lua.LVAsBool(enabledVal)
			}

			priority := 0
			if priorityVal := scriptTable.RawGetString("priority"); priorityVal != lua.LNil {
				if num, ok := priorityVal.(lua.LNumber); ok {
					priority = int(num)
				}
			}

			description := ""
			if descVal := scriptTable.RawGetString("description"); descVal != lua.LNil {
				description = descVal.String()
			}

			script := Script{
				Name:        name,
				Hook:        hookName,
				Source:      source,
				Enabled:     enabled,
				Priority:    priority,
				Description: description,
			}

			err := engine.AddScript(hookName, script)
			if err != nil {
				hostmodule.Logf(logger, context.Background(), "error", "hook.add_script error: %v", err)
				L.Push(lua.LBool(false))
				L.Push(lua.LString(err.Error()))
				return 2
			}

			hostmodule.Logf(logger, context.Background(), "debug", "Added script '%s' to hook: %s", name, hookName)
			L.Push(lua.LBool(true))
			return 1
		}))

		hookModule.RawSetString("list", L.NewFunction(func(L *lua.LState) int {
			hooks := engine.ListHooks()

			table := L.NewTable()
			for i, hookName := range hooks {
				table.RawSetInt(i+1, lua.LString(hookName))
			}

			L.Push(table)
			return 1
		}))

		L.Push(hookModule)
		return 1
	}
}
