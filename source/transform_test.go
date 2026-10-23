package source

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransformSource_ImplementsInterface(t *testing.T) {
	var _ Reader = (*TransformSource)(nil)
	var _ ReadWatcher = (*TransformSource)(nil)
}

func TestNewTransformSource_NilInner(t *testing.T) {
	_, err := NewTransformSource(nil, IdentityTransform)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inner reader is nil")
}

func TestNewTransformSource_NoTransforms(t *testing.T) {
	mem := NewMemSource()
	_, err := NewTransformSource(mem)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one transform")
}

func TestNewTransformSource_NilTransform(t *testing.T) {
	mem := NewMemSource()
	_, err := NewTransformSource(mem, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestLoad_SingleTransform(t *testing.T) {
	mem := NewMemSource()
	mem.Set("script.lua", "hello world")

	toUpper := TransformFunc(func(_ string, raw string) (string, error) {
		return strings.ToUpper(raw), nil
	})

	src, err := NewTransformSource(mem, toUpper)
	require.NoError(t, err)
	defer src.Close()

	code, err := src.Load(context.Background(), "script.lua")
	require.NoError(t, err)
	assert.Equal(t, "HELLO WORLD", code)
}

func TestLoad_ChainedTransforms(t *testing.T) {
	mem := NewMemSource()
	mem.Set("script.lua", "  hello  ")

	trim := TransformFunc(func(_ string, raw string) (string, error) {
		return strings.TrimSpace(raw), nil
	})
	toUpper := TransformFunc(func(_ string, raw string) (string, error) {
		return strings.ToUpper(raw), nil
	})

	src, err := NewTransformSource(mem, trim, toUpper)
	require.NoError(t, err)
	defer src.Close()

	code, err := src.Load(context.Background(), "script.lua")
	require.NoError(t, err)
	assert.Equal(t, "HELLO", code)
}

func TestLoad_TransformError(t *testing.T) {
	mem := NewMemSource()
	mem.Set("script.lua", "data")

	customErr := errors.New("decrypt failed")
	decrypt := TransformFunc(func(_ string, _ string) (string, error) {
		return "", customErr
	})

	src, err := NewTransformSource(mem, decrypt)
	require.NoError(t, err)
	defer src.Close()

	_, err = src.Load(context.Background(), "script.lua")
	require.Error(t, err)
	assert.ErrorIs(t, err, customErr)
	assert.Contains(t, err.Error(), "transform[0]")
}

func TestLoad_InnerError(t *testing.T) {
	mem := NewMemSource()

	src, err := NewTransformSource(mem, IdentityTransform)
	require.NoError(t, err)
	defer src.Close()

	_, err = src.Load(context.Background(), "missing.lua")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transform source: load")
}

func TestLoad_KeySpecificTransform(t *testing.T) {
	mem := NewMemSource()
	mem.Set("encrypted.lua", "ENCRYPTED_DATA")
	mem.Set("plain.lua", "print('hello')")

	decryptIfNeeded := TransformFunc(func(key string, raw string) (string, error) {
		if strings.HasPrefix(key, "encrypted") {
			runes := []rune(raw)
			for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
				runes[i], runes[j] = runes[j], runes[i]
			}
			return string(runes), nil
		}
		return raw, nil
	})

	src, err := NewTransformSource(mem, decryptIfNeeded)
	require.NoError(t, err)
	defer src.Close()

	code, err := src.Load(context.Background(), "encrypted.lua")
	require.NoError(t, err)
	assert.Equal(t, "ATAD_DETPYRCNE", code)

	code, err = src.Load(context.Background(), "plain.lua")
	require.NoError(t, err)
	assert.Equal(t, "print('hello')", code)
}

func TestTransformSource_Load_Concurrent(t *testing.T) {
	mem := NewMemSource()
	mem.Set("shared.lua", "body")

	src, err := NewTransformSource(mem, IdentityTransform)
	require.NoError(t, err)
	defer src.Close()

	const goroutines = 30
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			code, err := src.Load(context.Background(), "shared.lua")
			assert.NoError(t, err)
			assert.Equal(t, "body", code)
		}()
	}
	wg.Wait()
}

func TestTransformSource_Watch_Delegates(t *testing.T) {
	mem := NewMemSource()
	mem.Set("script.lua", "v1")

	src, err := NewTransformSource(mem, IdentityTransform)
	require.NoError(t, err)
	defer src.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := src.Watch(ctx, "script.lua")
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	mem.Set("script.lua", "v2")

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("timeout waiting for watch signal")
	}

	cancel()
}

func TestTransformSource_Watch_NotSupported(t *testing.T) {
	inner := &fakeSource{code: "x"}

	src, err := NewTransformSource(inner, IdentityTransform)
	require.NoError(t, err)
	defer src.Close()

	_, err = src.Watch(context.Background(), "k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not implement Watcher")
}

func TestThen_ChainsTransform(t *testing.T) {
	mem := NewMemSource()
	mem.Set("k", "hello")

	toUpper := TransformFunc(func(_ string, raw string) (string, error) {
		return strings.ToUpper(raw), nil
	})
	exclaim := TransformFunc(func(_ string, raw string) (string, error) {
		return raw + "!", nil
	})

	src, err := NewTransformSource(mem, toUpper)
	require.NoError(t, err)

	src2, err := src.Then(exclaim)
	require.NoError(t, err)
	defer src2.Close()

	code, err := src2.Load(context.Background(), "k")
	require.NoError(t, err)
	assert.Equal(t, "HELLO!", code)
}

func TestThen_NilTransform(t *testing.T) {
	mem := NewMemSource()
	src, err := NewTransformSource(mem, IdentityTransform)
	require.NoError(t, err)

	_, err = src.Then(nil)
	require.Error(t, err)
}

func TestIdentityTransform(t *testing.T) {
	result, err := IdentityTransform("key", "raw data")
	require.NoError(t, err)
	assert.Equal(t, "raw data", result)
}
