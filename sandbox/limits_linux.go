package sandbox

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// limitPlatformResources 使用 Linux 资源限制约束线程和匿名可写内存。
func limitPlatformResources() error {
	err := unix.Setrlimit(unix.RLIMIT_NPROC, &unix.Rlimit{Cur: 64, Max: 64})
	if err != nil {
		return fmt.Errorf("限制脚本进程数量失败: %w", err)
	}
	// DATA 覆盖匿名可写映射；AS 会误伤 Go 运行时预留的虚拟地址。
	err = unix.Setrlimit(unix.RLIMIT_DATA, &unix.Rlimit{Cur: 512 << 20, Max: 512 << 20})
	if err != nil {
		return fmt.Errorf("限制脚本内存失败: %w", err)
	}
	return nil
}
