package script_engine

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingLogger struct {
	mu      sync.Mutex
	records []logRecord
}

type logRecord struct {
	level string
	msg   string
	args  []any
}

func (r *recordingLogger) Debug(_ context.Context, msg string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, logRecord{"DEBUG", msg, args})
}

func (r *recordingLogger) Info(_ context.Context, msg string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, logRecord{"INFO", msg, args})
}

func (r *recordingLogger) Warn(_ context.Context, msg string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, logRecord{"WARN", msg, args})
}

func (r *recordingLogger) Error(_ context.Context, msg string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, logRecord{"ERROR", msg, args})
}

func (r *recordingLogger) With(...any) Logger {
	return r
}

func (r *recordingLogger) snapshot() []logRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]logRecord, len(r.records))
	copy(out, r.records)
	return out
}

// 编译期断言：实现满足目标接口。
var _ Logger = (*recordingLogger)(nil)

func withRestoredLogger(t *testing.T, tmp Logger, fn func()) {
	t.Helper()
	prev := GetLogger()
	SetLogger(tmp)
	defer SetLogger(prev)
	fn()
}

func TestNopLogger_Default(t *testing.T) {
	l := GetLogger()
	require.NotNil(t, l)

	assert.NotPanics(t, func() {
		l.Debug(nil, "debug")
		l.Info(nil, "info")
		l.Warn(nil, "warn")
		l.Error(nil, "error", "k", "v")
	})
}

func TestSetLogger_InjectionAndReset(t *testing.T) {
	original := GetLogger()

	rec := &recordingLogger{}
	withRestoredLogger(t, rec, func() {
		got := GetLogger()
		assert.True(t, got == rec, "GetLogger must return the injected instance")

		got.Error(nil, "boom", "code", 500)
		got.Warn(nil, "watch out")

		records := rec.snapshot()
		require.Len(t, records, 2)
		assert.Equal(t, "ERROR", records[0].level)
		assert.Equal(t, "boom", records[0].msg)
		assert.Equal(t, []any{"code", 500}, records[0].args)
		assert.Equal(t, "WARN", records[1].level)
		assert.Equal(t, "watch out", records[1].msg)
	})

	assert.True(t, GetLogger() == original, "original logger must be restored")

	SetLogger(nil)
	defer SetLogger(original)
	_, ok := GetLogger().(nopLogger)
	assert.True(t, ok, "SetLogger(nil) must revert to nopLogger")
}

func TestSlogLogger_OutputFormat(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := SlogLogger{L: slog.New(h)}

	ctx := context.Background()
	logger.Debug(ctx, "debug msg", "key", "value")
	logger.Info(ctx, "info msg")
	logger.Warn(ctx, "warn msg", "n", 42)
	logger.Error(ctx, "error msg", "err", "fail")

	output := buf.String()
	for _, want := range []string{"level=DEBUG", "level=INFO", "level=WARN", "level=ERROR"} {
		assert.True(t, strings.Contains(strings.ToLower(output), strings.ToLower(want)),
			"expected %q in output, got:\n%s", want, output)
	}
	assert.Contains(t, output, `msg="debug msg"`)
	assert.Contains(t, output, "key=value")
	assert.Contains(t, output, "n=42")
	assert.Contains(t, output, "err=fail")
}

func TestSlogLogger_NilContext(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := SlogLogger{L: slog.New(h)}

	assert.NotPanics(t, func() {
		logger.Debug(nil, "ok")
		logger.Info(nil, "ok")
		logger.Warn(nil, "ok")
		logger.Error(nil, "ok")
	})
	assert.Contains(t, buf.String(), "msg=ok")
}

func TestNewSlogLogger_Defaults(t *testing.T) {
	l := NewSlogLogger()
	require.NotNil(t, l)
	sl, ok := l.(SlogLogger)
	require.True(t, ok, "NewSlogLogger must return a SlogLogger")
	require.NotNil(t, sl.L)

	assert.NotPanics(t, func() {
		l.Error(context.Background(), "smoke", "k", 1)
	})
}

func TestSetLogger_Concurrent(t *testing.T) {
	prev := GetLogger()
	defer SetLogger(prev)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				SetLogger(&recordingLogger{})
			} else {
				_ = GetLogger()
			}
		}()
	}
	wg.Wait()

	SetLogger(nil)
	_, ok := GetLogger().(nopLogger)
	assert.True(t, ok)
}

func TestSlogLogger_With(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	base := SlogLogger{L: slog.New(h)}

	moduleLogger := base.With("module", "lua", "version", "5.1")

	ctx := context.Background()
	moduleLogger.Info(ctx, "engine initialized")
	moduleLogger.Error(ctx, "execution failed", "error", "timeout")

	output := buf.String()
	assert.Contains(t, output, "module=lua")
	assert.Contains(t, output, "version=5.1")
	assert.Contains(t, output, "msg=\"engine initialized\"")
	assert.Contains(t, output, "msg=\"execution failed\"")
	assert.Contains(t, output, "error=timeout")

	buf.Reset()
	base.Info(ctx, "original logger")
	output2 := buf.String()
	assert.Contains(t, output2, "msg=\"original logger\"")
	assert.NotContains(t, output2, "module=lua")
}

func TestSlogLogger_With_Chaining(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	base := SlogLogger{L: slog.New(h)}

	logger := base.With("module", "javascript").With("engine", "goja", "version", "v1.60.0")

	logger.Info(context.Background(), "chained attributes test")

	output := buf.String()
	assert.Contains(t, output, "module=javascript")
	assert.Contains(t, output, "engine=goja")
	assert.Contains(t, output, "version=v1.60.0")
}

func TestNopLogger_With(t *testing.T) {
	base := nopLogger{}
	withLogger := base.With("module", "test")

	_, ok := withLogger.(nopLogger)
	assert.True(t, ok, "nopLogger.With must return nopLogger")

	assert.NotPanics(t, func() {
		withLogger.Debug(nil, "debug")
		withLogger.Info(nil, "info")
		withLogger.Warn(nil, "warn")
		withLogger.Error(nil, "error")
	})
}
