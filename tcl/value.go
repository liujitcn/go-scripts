package tcl

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"modernc.org/libc"
	"modernc.org/tcl"
	libtcl "modernc.org/tcl/lib"
)

// newTclValue 将 JSON 上下文及宿主变量转换为原生 Tcl 对象，不把数据交给解释器当源码执行。
func newTclValue(interpreter *tcl.Interp, value any) (uintptr, error) {
	reflected := reflect.ValueOf(value)
	var err error
	if reflected.IsValid() && (reflected.Kind() == reflect.Map || reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array) {
		if _, bytes := value.([]byte); !bytes {
			list := libtcl.XTcl_NewListObj(interpreter.TLS(), 0, 0)
			items := make([]any, 0, reflected.Len())
			if reflected.Kind() == reflect.Map {
				if reflected.Type().Key().Kind() != reflect.String {
					return 0, fmt.Errorf("TCL 字典键必须为字符串")
				}
				keys := reflected.MapKeys()
				slices.SortFunc(keys, func(left, right reflect.Value) int { return strings.Compare(left.String(), right.String()) })
				for _, key := range keys {
					items = append(items, key.String(), reflected.MapIndex(key).Interface())
				}
			} else {
				for index := 0; index < reflected.Len(); index++ {
					items = append(items, reflected.Index(index).Interface())
				}
			}
			for _, item := range items {
				var child uintptr
				child, err = newTclValue(interpreter, item)
				if err != nil {
					return 0, err
				}
				if libtcl.XTcl_ListObjAppendElement(interpreter.TLS(), interpreter.Handle(), list, child) != libtcl.TCL_OK {
					return 0, fmt.Errorf("创建 TCL 列表失败")
				}
			}
			return list, nil
		}
	}
	text := ""
	if value != nil {
		text = fmt.Sprint(value)
		if boolean, ok := value.(bool); ok {
			text = "0"
			if boolean {
				text = "1"
			}
		}
		if bytes, ok := value.([]byte); ok {
			text = string(bytes)
		}
	}
	var pointer uintptr
	pointer, err = libc.CString(text)
	if err != nil {
		return 0, err
	}
	defer libc.Xfree(interpreter.TLS(), pointer)
	return libtcl.XTcl_NewStringObj(interpreter.TLS(), pointer, int32(len(text))), nil
}
