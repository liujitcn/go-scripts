package host

//

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"

	"github.com/liujitcn/go-scripts/hostmodule"
)

type recLogEntry struct {
	level string
	msg   string
}

type recordingLogger struct {
	entries []recLogEntry
}

func (r *recordingLogger) Debug(_ context.Context, msg string, _ ...any) {
	r.entries = append(r.entries, recLogEntry{"debug", msg})
}

func (r *recordingLogger) Info(_ context.Context, msg string, _ ...any) {
	r.entries = append(r.entries, recLogEntry{"info", msg})
}

func (r *recordingLogger) Warn(_ context.Context, msg string, _ ...any) {
	r.entries = append(r.entries, recLogEntry{"warn", msg})
}

func (r *recordingLogger) Error(_ context.Context, msg string, _ ...any) {
	r.entries = append(r.entries, recLogEntry{"error", msg})
}

func (r *recordingLogger) With(_ ...any) hostmodule.Logger { return r }

func (r *recordingLogger) snapshot() []recLogEntry {
	out := make([]recLogEntry, len(r.entries))
	copy(out, r.entries)
	return out
}

func TestConvertForFormat(t *testing.T) {
	t.Run("whole float converts to int", func(t *testing.T) {
		require.Equal(t, 42, convertForFormat(float64(42)))
		require.Equal(t, -3, convertForFormat(float64(-3)))
		require.Equal(t, 0, convertForFormat(float64(0)))
	})
	t.Run("fractional float passes through", func(t *testing.T) {
		require.Equal(t, float64(3.5), convertForFormat(float64(3.5)))
		require.Equal(t, float64(-0.25), convertForFormat(float64(-0.25)))
	})
	t.Run("Inf and NaN pass through", func(t *testing.T) {
		inf := convertForFormat(math.Inf(1))
		require.IsType(t, float64(0), inf)
		require.True(t, math.IsInf(inf.(float64), 1))
		nan := convertForFormat(math.NaN())
		require.IsType(t, float64(0), nan)
		require.True(t, math.IsNaN(nan.(float64)))
	})
	t.Run("non float values pass through unchanged", func(t *testing.T) {
		require.Equal(t, "str", convertForFormat("str"))
		require.Equal(t, 7, convertForFormat(7))
		require.Equal(t, true, convertForFormat(true))
		require.Equal(t, nil, convertForFormat(nil))
	})
}

func TestLoaderLogger_NonFormatLevels(t *testing.T) {
	rec := &recordingLogger{}
	L := lua.NewState()
	defer L.Close()
	RegisterLogger(L, rec)

	err := L.DoString(`
		local log = require "kratos_logger"
		log.info("m-info")
		log.warn("m-warn")
		log.error("m-error")
		log.debug("m-debug")
	`)
	require.NoError(t, err)
	require.Equal(t, []recLogEntry{
		{"info", "m-info"},
		{"warn", "m-warn"},
		{"error", "m-error"},
		{"debug", "m-debug"},
	}, rec.snapshot())
}

func TestLoaderLogger_FormatArgsConversion(t *testing.T) {
	rec := &recordingLogger{}
	L := lua.NewState()
	defer L.Close()
	RegisterLogger(L, rec)

	err := L.DoString(`
		local log = require "kratos_logger"
		log.infof("n=%d", 3)
		log.warnf("a=%d b=%d", 1, 2)
		log.errorf("f=%v", 3.5)
		log.debugf("s=%s!", "txt")
		log.infof("plain-no-args")
	`)
	require.NoError(t, err)
	require.Equal(t, []recLogEntry{
		{"info", "n=3"},
		{"warn", "a=1 b=2"},
		{"error", "f=3.5"},
		{"debug", "s=txt!"},
		{"info", "plain-no-args"},
	}, rec.snapshot())
}
