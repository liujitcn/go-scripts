//go:build !darwin && !linux

package sandbox

import (
	"context"
	"fmt"
	"os/exec"
)

// newCommand 拒绝在不具备操作系统沙箱的环境中降级执行用户源码。
func newCommand(context.Context, string, Options) (*exec.Cmd, error) {
	return nil, fmt.Errorf("当前系统没有受支持的脚本沙箱，请使用 Linux 或 macOS 执行环境")
}

// configureProcess 为不支持的平台保留编译入口，执行会由 newCommand 拒绝。
func configureProcess(*exec.Cmd) {}

// LimitWorker 拒绝缺少系统隔离能力的工作进程。
func LimitWorker() error {
	return fmt.Errorf("当前系统不支持脚本沙箱")
}
