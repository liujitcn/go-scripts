package host

import (
	"context"
	"math"

	lua "github.com/yuin/gopher-lua"

	"github.com/liujitcn/go-scripts/hostmodule"
	"github.com/liujitcn/go-scripts/lua/convert"
)

func convertForFormat(val interface{}) interface{} {
	if f, ok := val.(float64); ok {

		if f == math.Floor(f) && !math.IsInf(f, 0) && !math.IsNaN(f) {
			return int(f)
		}
	}
	return val
}

func RegisterLogger(L *lua.LState, logger hostmodule.Logger) {

	L.PreloadModule("kratos_logger", LoaderLogger(logger))
}

func LoaderLogger(logger hostmodule.Logger) lua.LGFunction {
	return func(L *lua.LState) int {

		logModule := L.NewTable()

		logModule.RawSetString("info", L.NewFunction(func(L *lua.LState) int {
			msg := L.CheckString(1)
			if logger != nil {
				logger.Info(context.Background(), msg)
			}
			return 0
		}))

		logModule.RawSetString("warn", L.NewFunction(func(L *lua.LState) int {
			msg := L.CheckString(1)
			if logger != nil {
				logger.Warn(context.Background(), msg)
			}
			return 0
		}))

		logModule.RawSetString("error", L.NewFunction(func(L *lua.LState) int {
			msg := L.CheckString(1)
			if logger != nil {
				logger.Error(context.Background(), msg)
			}
			return 0
		}))

		logModule.RawSetString("debug", L.NewFunction(func(L *lua.LState) int {
			msg := L.CheckString(1)
			if logger != nil {
				logger.Debug(context.Background(), msg)
			}
			return 0
		}))

		logModule.RawSetString("infof", L.NewFunction(func(L *lua.LState) int {
			format := L.CheckString(1)
			args := make([]interface{}, L.GetTop()-1)
			for i := 2; i <= L.GetTop(); i++ {
				args[i-2] = convertForFormat(convert.ToGoValue(L.Get(i)))
			}
			hostmodule.Logf(logger, context.Background(), "info", format, args...)
			return 0
		}))

		logModule.RawSetString("errorf", L.NewFunction(func(L *lua.LState) int {
			format := L.CheckString(1)
			args := make([]interface{}, L.GetTop()-1)
			for i := 2; i <= L.GetTop(); i++ {
				args[i-2] = convertForFormat(convert.ToGoValue(L.Get(i)))
			}
			hostmodule.Logf(logger, context.Background(), "error", format, args...)
			return 0
		}))

		logModule.RawSetString("warnf", L.NewFunction(func(L *lua.LState) int {
			format := L.CheckString(1)
			args := make([]interface{}, L.GetTop()-1)
			for i := 2; i <= L.GetTop(); i++ {
				args[i-2] = convertForFormat(convert.ToGoValue(L.Get(i)))
			}
			hostmodule.Logf(logger, context.Background(), "warn", format, args...)
			return 0
		}))

		logModule.RawSetString("debugf", L.NewFunction(func(L *lua.LState) int {
			format := L.CheckString(1)
			args := make([]interface{}, L.GetTop()-1)
			for i := 2; i <= L.GetTop(); i++ {
				args[i-2] = convertForFormat(convert.ToGoValue(L.Get(i)))
			}
			hostmodule.Logf(logger, context.Background(), "debug", format, args...)
			return 0
		}))

		L.Push(logModule)
		return 1
	}
}
