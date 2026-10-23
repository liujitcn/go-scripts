package hostmodule

import (
	"context"
	"fmt"
	"log/slog"

	kitlogger "github.com/liujitcn/kratos-kit/logger"
)

// Logger 定义宿主模块所需的最小日志接口。
type Logger interface {
	Debug(ctx context.Context, msg string, args ...any)
	Info(ctx context.Context, msg string, args ...any)
	Warn(ctx context.Context, msg string, args ...any)
	Error(ctx context.Context, msg string, args ...any)
}

type kitLogger struct {
	logger *slog.Logger
}

// NewKitLogger 创建基于 kratos-kit/logger 的标准日志适配器。
func NewKitLogger() Logger {
	return &kitLogger{logger: kitlogger.NewStdLogger()}
}

func (l *kitLogger) Debug(ctx context.Context, msg string, args ...any) {
	l.logger.DebugContext(ctx, msg, args...)
}

func (l *kitLogger) Info(ctx context.Context, msg string, args ...any) {
	l.logger.InfoContext(ctx, msg, args...)
}

func (l *kitLogger) Warn(ctx context.Context, msg string, args ...any) {
	l.logger.WarnContext(ctx, msg, args...)
}

func (l *kitLogger) Error(ctx context.Context, msg string, args ...any) {
	l.logger.ErrorContext(ctx, msg, args...)
}

func (l *kitLogger) With(args ...any) Logger {
	return &kitLogger{logger: l.logger.With(args...)}
}

// Logf 按 level 格式化消息并转发给 Logger。
func Logf(logger Logger, ctx context.Context, level string, format string, args ...any) {
	if logger == nil {
		return
	}
	message := fmt.Sprintf(format, args...)
	switch level {
	case "debug":
		logger.Debug(ctx, message)
	case "info":
		logger.Info(ctx, message)
	case "warn":
		logger.Warn(ctx, message)
	case "error":
		logger.Error(ctx, message)
	}
}
