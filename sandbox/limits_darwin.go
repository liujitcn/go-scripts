package sandbox

// limitPlatformResources 保留 macOS 沙箱策略和 Go 运行时内存预算，不套用 Linux 专有资源项。
func limitPlatformResources() error { return nil }
