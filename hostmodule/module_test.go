package hostmodule

//

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
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

func (r *recordingLogger) With(_ ...any) *recordingLogger { return r }

func (r *recordingLogger) snapshot() []recLogEntry {
	out := make([]recLogEntry, len(r.entries))
	copy(out, r.entries)
	return out
}

func TestConvertForFormatVal(t *testing.T) {
	t.Run("whole float converts to int", func(t *testing.T) {
		require.Equal(t, 42, convertForFormatVal(float64(42)))
		require.Equal(t, -3, convertForFormatVal(float64(-3)))
	})
	t.Run("fractional float passes through", func(t *testing.T) {
		require.Equal(t, float64(3.5), convertForFormatVal(float64(3.5)))
	})
	t.Run("Inf and NaN pass through", func(t *testing.T) {
		inf := convertForFormatVal(math.Inf(-1))
		require.IsType(t, float64(0), inf)
		require.True(t, math.IsInf(inf.(float64), -1))
		nan := convertForFormatVal(math.NaN())
		require.IsType(t, float64(0), nan)
		require.True(t, math.IsNaN(nan.(float64)))
	})
	t.Run("non float values pass through unchanged", func(t *testing.T) {
		require.Equal(t, "str", convertForFormatVal("str"))
		require.Equal(t, true, convertForFormatVal(true))
		require.Equal(t, nil, convertForFormatVal(nil))
	})
}

func TestConvertForFormatArgs(t *testing.T) {
	t.Run("mixed types converted element-wise", func(t *testing.T) {
		got := convertForFormatArgs([]any{float64(1), "a", float64(2.5), true})
		require.Equal(t, int(1), got[0])
		require.Equal(t, "a", got[1])
		require.Equal(t, float64(2.5), got[2])
		require.Equal(t, true, got[3])
		require.Len(t, got, 4)
	})
	t.Run("empty slice stays empty", func(t *testing.T) {
		got := convertForFormatArgs([]any{})
		require.Empty(t, got)
	})
	t.Run("whole floats all converted", func(t *testing.T) {
		got := convertForFormatArgs([]any{float64(-7), float64(100)})
		require.Equal(t, int(-7), got[0])
		require.Equal(t, int(100), got[1])
	})
}

func TestModuleLogger_Structure(t *testing.T) {
	rec := &recordingLogger{}
	mod := ModuleLogger(rec)

	require.Equal(t, "log", mod.Name)
	require.Len(t, mod.Funcs, 8)
	for _, name := range []string{"info", "warn", "error", "debug", "infof", "warnf", "errorf", "debugf"} {
		fn, ok := mod.Funcs[name]
		require.True(t, ok, "ModuleLogger should expose %q", name)
		require.Equal(t, reflect.Func, reflect.TypeOf(fn).Kind(), "Funcs[%q] should be a function", name)
	}
}

func TestModuleLogger_Invocation(t *testing.T) {
	rec := &recordingLogger{}
	mod := ModuleLogger(rec)

	info, ok := mod.Funcs["info"].(func(string))
	require.True(t, ok)
	info("go-info")
	warn, ok := mod.Funcs["warn"].(func(string))
	require.True(t, ok)
	warn("go-warn")
	errFn, ok := mod.Funcs["error"].(func(string))
	require.True(t, ok)
	errFn("go-error")
	dbg, ok := mod.Funcs["debug"].(func(string))
	require.True(t, ok)
	dbg("go-debug")

	infof, ok := mod.Funcs["infof"].(func(string, ...any))
	require.True(t, ok)
	infof("i=%d", float64(3))
	warnf, ok := mod.Funcs["warnf"].(func(string, ...any))
	require.True(t, ok)
	warnf("w=%s%d", "x", float64(9))
	errorf, ok := mod.Funcs["errorf"].(func(string, ...any))
	require.True(t, ok)
	errorf("e=%v", float64(2.5))
	debugf, ok := mod.Funcs["debugf"].(func(string, ...any))
	require.True(t, ok)
	debugf("d=%v", math.NaN())

	require.Equal(t, []recLogEntry{
		{"info", "go-info"},
		{"warn", "go-warn"},
		{"error", "go-error"},
		{"debug", "go-debug"},
		{"info", "i=3"},
		{"warn", "w=x9"},
		{"error", "e=2.5"},
		{"debug", "d=NaN"},
	}, rec.snapshot())
}
