package host

import (
	"context"
	"time"

	lua "github.com/yuin/gopher-lua"

	"github.com/liujitcn/go-scripts/hostmodule"
)

func RegisterUtilAPI(L *lua.LState, logger hostmodule.Logger) {

	L.PreloadModule("kratos_util", LoaderUtil(logger))

	if logger != nil {
		hostmodule.Logf(logger, context.Background(), "debug", "Registered Lua util API")
	}
}

func LoaderUtil(logger hostmodule.Logger, maxSleep ...time.Duration) lua.LGFunction {
	cap := 5 * time.Second
	if len(maxSleep) > 0 && maxSleep[0] > 0 {
		cap = maxSleep[0]
	}
	return func(L *lua.LState) int {

		utilModule := L.NewTable()

		utilModule.RawSetString("sleep", L.NewFunction(func(L *lua.LState) int {
			seconds := L.CheckNumber(1)
			duration := time.Duration(float64(seconds) * float64(time.Second))
			if duration > cap {
				if logger != nil {
					hostmodule.Logf(logger, context.Background(), "warn", "Lua sleep %v exceeds cap, clamped to %v", duration, cap)
				}
				duration = cap
			}

			if logger != nil {
				hostmodule.Logf(logger, context.Background(), "debug", "Lua sleep: %v", duration)
			}

			time.Sleep(duration)
			return 0
		}))

		utilModule.RawSetString("time", L.NewFunction(func(L *lua.LState) int {
			L.Push(lua.LNumber(time.Now().Unix()))
			return 1
		}))

		utilModule.RawSetString("timestamp", L.NewFunction(func(L *lua.LState) int {
			L.Push(lua.LNumber(time.Now().UnixMilli()))
			return 1
		}))

		utilModule.RawSetString("date", L.NewFunction(func(L *lua.LState) int {
			format := L.OptString(1, time.RFC3339)
			L.Push(lua.LString(time.Now().Format(format)))
			return 1
		}))

		L.Push(utilModule)
		return 1
	}
}
