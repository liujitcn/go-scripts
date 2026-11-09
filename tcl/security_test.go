package tcl

import (
	"context"
	"testing"
	"time"
)

// TestSafeInterpreter 验证语言计算保留，系统命令、文件和网络能力不暴露给用户脚本。
func TestSafeInterpreter(t *testing.T) {
	engine, err := newTCLEngine()
	if err != nil {
		t.Fatal(err)
	}
	err = engine.Init(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	for _, source := range []string{"exec id", "open /etc/passwd", "socket 127.0.0.1 80", "interp invokehidden {} exec id"} {
		_, err = engine.ExecuteString(context.Background(), "unsafe", source)
		if err == nil {
			t.Fatalf("越界脚本未被拒绝: %s", source)
		}
	}
	var result any
	result, err = engine.ExecuteString(context.Background(), "safe", "expr {6 * 7}")
	if err != nil || result != int64(42) {
		t.Fatalf("计算功能回归: %v, %v", result, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = engine.ExecuteString(ctx, "loop", "while {1} {}")
	if err == nil {
		t.Fatal("死循环未中断")
	}
}

// TestGlobalDataIsNotCode 验证变量内容保持原样，字典上下文可用且不会执行数据中的命令。
func TestGlobalDataIsNotCode(t *testing.T) {
	engine, err := newTCLEngine()
	if err != nil {
		t.Fatal(err)
	}
	err = engine.Init(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	value := "][set injected 1]; {x}\n$env(HOME)"
	err = engine.RegisterGlobal("ctx", map[string]any{"message": value})
	if err != nil {
		t.Fatal(err)
	}
	var result any
	result, err = engine.ExecuteString(context.Background(), "data", "dict get $ctx message")
	if err != nil || result != value {
		t.Fatalf("上下文被改变: %v, %v", result, err)
	}
	_, err = engine.GetGlobal("injected")
	if err == nil {
		t.Fatal("变量内容被当作源码执行")
	}
}
