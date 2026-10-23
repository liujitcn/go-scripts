package convert

import (
	"fmt"
	"reflect"

	lua "github.com/yuin/gopher-lua"
)

//

func ToLuaValue(L *lua.LState, val any) lua.LValue {
	if val == nil {
		return lua.LNil
	}

	if lv, ok := val.(lua.LValue); ok {
		return lv
	}

	switch v := val.(type) {
	case bool:
		return lua.LBool(v)
	case string:
		return lua.LString(v)
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
	case uint:
		return lua.LNumber(v)
	case uint8:
		return lua.LNumber(v)
	case uint16:
		return lua.LNumber(v)
	case uint32:
		return lua.LNumber(v)
	case uint64:
		return lua.LNumber(v)
	case float32:
		return lua.LNumber(v)
	case float64:
		return lua.LNumber(v)
	case map[string]any:
		return tableFromMap(L, v)
	case []any:
		return tableFromSlice(L, v)
	default:

		return reflectToLua(L, val)
	}
}

//

func ToGoValue(val lua.LValue) any {
	if val == nil || val == lua.LNil {
		return nil
	}

	switch v := val.(type) {
	case lua.LBool:
		return bool(v)
	case lua.LNumber:
		return float64(v)
	case lua.LString:
		return string(v)
	case *lua.LTable:
		return tableToGo(v)
	default:
		return nil
	}
}

func tableFromMap(L *lua.LState, m map[string]any) *lua.LTable {
	table := L.NewTable()
	for key, value := range m {
		table.RawSetString(key, ToLuaValue(L, value))
	}
	return table
}

func tableFromSlice(L *lua.LState, s []any) *lua.LTable {
	table := L.NewTable()
	for i, value := range s {
		table.RawSetInt(i+1, ToLuaValue(L, value))
	}
	return table
}

//

func tableToGo(t *lua.LTable) any {
	maxN := t.MaxN()
	if maxN > 0 {

		arr := make([]any, 0, maxN)
		for i := 1; i <= maxN; i++ {
			val := t.RawGetInt(i)
			arr = append(arr, ToGoValue(val))
		}
		return arr
	}

	m := make(map[string]any)
	t.ForEach(func(k, val lua.LValue) {
		if key, ok := k.(lua.LString); ok {
			m[string(key)] = ToGoValue(val)
		}
	})
	return m
}

func ToString(val lua.LValue) string {
	return lua.LVAsString(val)
}

func ToNumber(val lua.LValue) float64 {
	return float64(lua.LVAsNumber(val))
}

func ToBool(val lua.LValue) bool {
	return lua.LVAsBool(val)
}

func reflectToLua(L *lua.LState, val any) lua.LValue {
	if val == nil {
		return lua.LNil
	}

	v := reflect.ValueOf(val)

	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return lua.LNil
		}
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Bool:
		return lua.LBool(v.Bool())

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return lua.LNumber(v.Int())

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return lua.LNumber(v.Uint())

	case reflect.Float32, reflect.Float64:
		return lua.LNumber(v.Float())

	case reflect.String:
		return lua.LString(v.String())

	case reflect.Slice, reflect.Array:
		return reflectSliceToLua(L, v)

	case reflect.Map:
		return reflectMapToLua(L, v)

	case reflect.Struct:
		return reflectStructToLua(L, v)

	case reflect.Interface:

		if v.IsNil() {
			return lua.LNil
		}
		return reflectToLua(L, v.Interface())

	default:

		return lua.LString(fmt.Sprintf("%v", val))
	}
}

func reflectStructToLua(L *lua.LState, v reflect.Value) *lua.LTable {
	table := L.NewTable()
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		fieldValue := v.Field(i)

		if !field.IsExported() {
			continue
		}

		key := field.Name

		luaValue := reflectToLua(L, fieldValue.Interface())
		table.RawSetString(key, luaValue)
	}

	return table
}

func reflectMapToLua(L *lua.LState, v reflect.Value) *lua.LTable {
	table := L.NewTable()

	iter := v.MapRange()
	for iter.Next() {
		key := iter.Key()
		val := iter.Value()

		var keyStr string
		switch key.Kind() {
		case reflect.String:
			keyStr = key.String()
		default:
			keyStr = fmt.Sprintf("%v", key.Interface())
		}

		luaValue := reflectToLua(L, val.Interface())
		table.RawSetString(keyStr, luaValue)
	}

	return table
}

func reflectSliceToLua(L *lua.LState, v reflect.Value) *lua.LTable {
	table := L.NewTable()

	for i := 0; i < v.Len(); i++ {
		elem := v.Index(i)
		luaValue := reflectToLua(L, elem.Interface())
		table.RawSetInt(i+1, luaValue) // Lua uses 1-based indexing
	}

	return table
}
