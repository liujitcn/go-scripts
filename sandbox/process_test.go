package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// processFixture 传递测试所需的动作和结果，不依赖真实服务凭据。
type processFixture struct {
	Action string
	Path   string
	Value  string
	Error  string
}

// TestMain 在同一测试二进制中提供工作进程入口，验证实际系统沙箱而非模拟执行。
func TestMain(main *testing.M) {
	if !IsWorker() {
		os.Exit(main.Run())
	}
	fixture := processFixture{}
	err := LimitWorker()
	if err == nil {
		err = json.NewDecoder(os.Stdin).Decode(&fixture)
	}
	if err == nil {
		switch fixture.Action {
		case "read":
			var data []byte
			data, err = os.ReadFile(fixture.Path)
			fixture.Value = string(data)
		case "connect":
			var connection net.Conn
			connection, err = net.Dial("unix", fixture.Path)
			if err == nil {
				err = connection.Close()
			}
		case "loop":
			for {
			}
		case "output":
			fixture.Value = strings.Repeat("x", 4096)
		}
	}
	if err != nil {
		fixture.Error = err.Error()
	}
	var data []byte
	data, err = json.Marshal(fixture)
	if err == nil {
		err = WriteResult(data)
	}
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// TestProcessIsolation 验证宿主文件默认不可读、明确授权可读，并且不会继承主服务秘密。
func TestProcessIsolation(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("当前平台不提供脚本沙箱")
	}
	directory := t.TempDir()
	secret := filepath.Join(directory, "secret")
	err := os.WriteFile(secret, []byte("private-value"), 0600)
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(directory, "work")
	err = os.Mkdir(work, 0700)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var input, output []byte
	input, err = json.Marshal(processFixture{Action: "read", Path: secret})
	if err != nil {
		t.Fatal(err)
	}
	output, err = Run(ctx, input, Options{Directory: work})
	if err != nil {
		t.Fatal(err)
	}
	var fixture processFixture
	err = json.Unmarshal(output, &fixture)
	if err != nil || fixture.Error == "" || fixture.Value != "" {
		t.Fatalf("未授权文件泄露: %s, %v", output, err)
	}
	output, err = Run(ctx, input, Options{Directory: work, ReadOnlyPaths: []string{secret}})
	if err != nil {
		t.Fatal(err)
	}
	err = json.Unmarshal(output, &fixture)
	if err != nil || fixture.Value != "private-value" {
		t.Fatalf("授权文件访问失败: %s, %v", output, err)
	}
	var listener net.Listener
	socket := filepath.Join(directory, "private.sock")
	listener, err = net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	input, err = json.Marshal(processFixture{Action: "connect", Path: socket})
	if err != nil {
		t.Fatal(err)
	}
	output, err = Run(ctx, input, Options{Directory: work})
	if err != nil {
		t.Fatal(err)
	}
	err = json.Unmarshal(output, &fixture)
	if err != nil || fixture.Error == "" {
		t.Fatalf("未授权套接字可访问: %s, %v", output, err)
	}
	output, err = Run(ctx, input, Options{Directory: work, ReadOnlyPaths: []string{socket}})
	if err != nil {
		t.Fatal(err)
	}
	err = json.Unmarshal(output, &fixture)
	if err != nil || fixture.Error != "" {
		t.Fatalf("授权套接字不可访问: %s, %v", output, err)
	}
}

// TestProcessBudget 验证真实死循环和输出膨胀会结束，不让后台任务继续消耗宿主资源。
func TestProcessBudget(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("当前平台不提供脚本沙箱")
	}
	for _, action := range []string{"loop", "output"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
			defer cancel()
			_, err := Run(ctx, []byte(fmt.Sprintf(`{"Action":%q}`, action)), Options{Directory: t.TempDir(), MaxOutputBytes: 1024})
			if err == nil {
				t.Fatal("超限任务未被拒绝")
			}
		})
	}
}
