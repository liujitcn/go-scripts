package host

import (
	"context"
	"fmt"
	"strings"

	hostmodule "github.com/liujitcn/go-scripts/hostmodule"
	lua "github.com/yuin/gopher-lua"
)

func RegisterHTTP(L *lua.LState, opts hostmodule.HTTPOptions) {
	L.PreloadModule("kratos_http", LoaderHTTP(opts))
}

func LoaderHTTP(opts hostmodule.HTTPOptions) lua.LGFunction {
	normalized := hostmodule.NormalizeHTTPOptions(opts)
	client := hostmodule.BuildHTTPClient(normalized)

	return func(L *lua.LState) int {
		httpModule := L.NewTable()

		httpModule.RawSetString("request", L.NewFunction(func(L *lua.LState) int {
			rawURL := L.CheckString(1)
			if err := hostmodule.CheckAllowedURL(rawURL, normalized.AllowedDomains, normalized.DenyLoopback); err != nil {
				L.RaiseError("http blocked: %v", err)
				return 0
			}

			method := "GET"
			headerMap := map[string]string{}
			body := ""
			if options := L.OptTable(2, nil); options != nil {
				if m := options.RawGetString("method"); m != lua.LNil {
					method = strings.ToUpper(m.String())
				}
				if h, ok := options.RawGetString("headers").(*lua.LTable); ok {
					h.ForEach(func(k, v lua.LValue) {
						headerMap[k.String()] = v.String()
					})
				}
				if b := options.RawGetString("body"); b != lua.LNil {
					body = b.String()
				}
			}

			result, err := hostmodule.HTTPRequest(context.Background(), client, rawURL, method, headerMap, body, normalized.MaxResponseBody)
			if err != nil {
				L.RaiseError("http request failed: %v", err)
				return 0
			}
			L.Push(luaTableFromMap(L, result))
			return 1
		}))

		httpModule.RawSetString("get", L.NewFunction(func(L *lua.LState) int {
			rawURL := L.CheckString(1)
			if err := hostmodule.CheckAllowedURL(rawURL, normalized.AllowedDomains, normalized.DenyLoopback); err != nil {
				L.RaiseError("http blocked: %v", err)
				return 0
			}
			result, err := hostmodule.HTTPRequest(context.Background(), client, rawURL, "GET", nil, "", normalized.MaxResponseBody)
			if err != nil {
				L.RaiseError("http request failed: %v", err)
				return 0
			}
			L.Push(luaTableFromMap(L, result))
			return 1
		}))

		httpModule.RawSetString("post", L.NewFunction(func(L *lua.LState) int {
			rawURL := L.CheckString(1)
			body := L.CheckString(2)
			contentType := L.OptString(3, "application/json")
			if err := hostmodule.CheckAllowedURL(rawURL, normalized.AllowedDomains, normalized.DenyLoopback); err != nil {
				L.RaiseError("http blocked: %v", err)
				return 0
			}
			result, err := hostmodule.HTTPRequest(context.Background(), client, rawURL, "POST",
				map[string]string{"Content-Type": contentType}, body, normalized.MaxResponseBody)
			if err != nil {
				L.RaiseError("http request failed: %v", err)
				return 0
			}
			L.Push(luaTableFromMap(L, result))
			return 1
		}))

		L.Push(httpModule)
		return 1
	}
}

func luaTableFromMap(L *lua.LState, m map[string]any) *lua.LTable {
	t := L.NewTable()
	for k, v := range m {
		switch val := v.(type) {
		case string:
			t.RawSetString(k, lua.LString(val))
		case int:
			t.RawSetString(k, lua.LNumber(val))
		case int64:
			t.RawSetString(k, lua.LNumber(val))
		case map[string]string:
			t.RawSetString(k, luaTableFromStrings(L, val))
		default:
			t.RawSetString(k, lua.LString(fmt.Sprintf("%v", val)))
		}
	}
	return t
}

func luaTableFromStrings(L *lua.LState, m map[string]string) *lua.LTable {
	t := L.NewTable()
	for k, v := range m {
		t.RawSetString(k, lua.LString(v))
	}
	return t
}
