package script_engine

// Type 标识脚本引擎实现类型。
type Type string

const (
	UnknownType Type = "unknown"

	// LuaType 表示 gopher-lua 引擎。
	LuaType Type = "lua"

	// JavaScriptType 表示 goja JavaScript 引擎。
	JavaScriptType Type = "javascript"

	// GPythonType 表示 gpython Python 引擎。
	GPythonType Type = "gpython"

	// YaegiType 表示 Yaegi Go 解释器引擎。
	YaegiType Type = "yaegi"

	// WazeroType 表示 wazero WebAssembly 引擎。
	WazeroType Type = "wazero"

	// CELType 表示 cel-go CEL 表达式引擎。
	CELType Type = "cel"

	// ExprType 表示 expr-lang/expr 表达式引擎。
	ExprType Type = "expr"

	// StarlarkType 表示 starlark-go 引擎。
	StarlarkType Type = "starlark"

	// TclType 表示 modernc.org/tcl 引擎。
	TclType Type = "tcl"
)
