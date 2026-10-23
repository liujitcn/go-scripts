package script_engine

import (
	"context"
	"log/slog"
	"os"
	"sync"
)

// Logger 是框架内部使用的最小日志接口，便于适配 slog、zap、zerolog 等后端。
type Logger interface {
	// Debug 记录 DEBUG 级别日志。
	Debug(ctx context.Context, msg string, args ...any)
	// Info 记录 INFO 级别日志。
	Info(ctx context.Context, msg string, args ...any)
	// Warn 记录 WARN 级别日志。
	Warn(ctx context.Context, msg string, args ...any)
	// Error 记录 ERROR 级别日志。
	Error(ctx context.Context, msg string, args ...any)

	// With 返回附带结构化字段的新日志器。
	With(args ...any) Logger
}

// nopLogger 丢弃全部日志，是默认的无副作用实现。
type nopLogger struct{}

func (nopLogger) Debug(context.Context, string, ...any) {}
func (nopLogger) Info(context.Context, string, ...any)  {}
func (nopLogger) Warn(context.Context, string, ...any)  {}
func (nopLogger) Error(context.Context, string, ...any) {}

// With 返回新的 nopLogger；参数不会产生输出。
func (nopLogger) With(...any) Logger { return nopLogger{} }

// 编译期断言：实现满足目标接口。
var _ Logger = nopLogger{}

// SlogLogger 将标准库 slog.Logger 适配为 Logger 接口。
// L 不应为 nil；NewSlogLogger 会返回可直接使用的实例。

type SlogLogger struct {
	// L 是底层 slog 日志器。

	L *slog.Logger
}

// Debug 转发到 slog.Logger.DebugContext。
func (s SlogLogger) Debug(ctx context.Context, msg string, args ...any) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.L.DebugContext(ctx, msg, args...)
}

// Info 转发到 slog.Logger.InfoContext。
func (s SlogLogger) Info(ctx context.Context, msg string, args ...any) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.L.InfoContext(ctx, msg, args...)
}

// Warn 转发到 slog.Logger.WarnContext。
func (s SlogLogger) Warn(ctx context.Context, msg string, args ...any) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.L.WarnContext(ctx, msg, args...)
}

// Error 转发到 slog.Logger.ErrorContext。
func (s SlogLogger) Error(ctx context.Context, msg string, args ...any) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.L.ErrorContext(ctx, msg, args...)
}

// With 返回底层日志器附带字段的新 SlogLogger。
func (s SlogLogger) With(args ...any) Logger {
	return SlogLogger{L: s.L.With(args...)}
}

// 编译期断言：实现满足目标接口。
var _ Logger = SlogLogger{}

// NewSlogLogger 创建向 stderr 输出文本日志的默认 SlogLogger。
func NewSlogLogger() Logger {
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	return SlogLogger{L: slog.New(h)}
}

// globalLogger 保存当前包级日志器。
var (
	globalLoggerMu sync.RWMutex
	globalLogger   Logger = nopLogger{}
)

// SetLogger 设置包级日志器；传入 nil 恢复静默实现。
// 此方法可与日志调用并发使用；修改日志器自身状态时仍需由调用方负责同步。
func SetLogger(l Logger) {
	globalLoggerMu.Lock()
	defer globalLoggerMu.Unlock()
	if l == nil {
		globalLogger = nopLogger{}
		return
	}
	globalLogger = l
}

// GetLogger 返回当前包级日志器。
func GetLogger() Logger {
	globalLoggerMu.RLock()
	defer globalLoggerMu.RUnlock()
	return globalLogger
}
