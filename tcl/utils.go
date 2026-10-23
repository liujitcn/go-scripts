package tcl

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

func goToTCLString(v any) string {
	if v == nil {
		return "\"\""
	}
	switch val := v.(type) {
	case nil:
		return "\"\""
	case bool:
		if val {
			return "1"
		}
		return "0"
	case int:
		return strconv.Itoa(val)
	case int32:
		return strconv.FormatInt(int64(val), 10)
	case int64:
		return strconv.FormatInt(val, 10)
	case uint:
		return strconv.FormatUint(uint64(val), 10)
	case uint32:
		return strconv.FormatUint(uint64(val), 10)
	case uint64:
		return strconv.FormatUint(val, 10)
	case float32:
		return strconv.FormatFloat(float64(val), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case string:
		return tclQuote(val)
	case []byte:
		return tclQuote(string(val))
	case []string:
		parts := make([]string, len(val))
		for i, s := range val {
			parts[i] = tclQuote(s)
		}
		return "{" + strings.Join(parts, " ") + "}"
	case []any:
		parts := make([]string, len(val))
		for i, item := range val {
			parts[i] = goToTCLString(item)
		}
		return "{" + strings.Join(parts, " ") + "}"
	default:
		return tclQuote(fmt.Sprint(v))
	}
}

func tclQuote(s string) string {

	if s == "" {
		return "\"\""
	}

	if strings.ContainsAny(s, " \t\n;[]$\\\"{}") {

		if !strings.ContainsAny(s, "{}") {
			return "{" + s + "}"
		}

		s = strings.ReplaceAll(s, "\\", "\\\\")
		s = strings.ReplaceAll(s, "\"", "\\\"")
		s = strings.ReplaceAll(s, "$", "\\$")
		s = strings.ReplaceAll(s, "[", "\\[")
		s = strings.ReplaceAll(s, "]", "\\]")
		return "\"" + s + "\""
	}
	return s
}

func parseTCLValue(s string) any {
	if s == "" {
		return ""
	}

	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}

	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}

	switch s {
	case "1", "true", "True", "TRUE", "yes", "on":

		if s == "1" {
			return true
		}
	case "0", "false", "False", "FALSE", "no", "off":
		if s == "0" {
			return false
		}
	}
	return s
}

func sanitizeCmdName(name string) string {
	return strings.ReplaceAll(name, ".", "::")
}

func callHostFunc(fn any, args []any) any {
	fnVal := reflect.ValueOf(fn)
	fnType := fnVal.Type()

	argVals := make([]reflect.Value, fnType.NumIn())
	for i := 0; i < fnType.NumIn(); i++ {
		if i < len(args) {
			argVals[i] = reflect.ValueOf(args[i])
			if argVals[i].Type() != fnType.In(i) {
				if argVals[i].Type().ConvertibleTo(fnType.In(i)) {
					argVals[i] = argVals[i].Convert(fnType.In(i))
				}
			}
		}
	}

	results := fnVal.Call(argVals)
	if len(results) == 0 {
		return nil
	}
	return results[0].Interface()
}
