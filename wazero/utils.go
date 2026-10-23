package wazero

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
)

////////////////////////////////////////////////////////////////////////////////
// 内部辅助函数。
////////////////////////////////////////////////////////////////////////////////

func (e *engine) instantiateAndRun(ctx context.Context, compiled wazero.CompiledModule) (any, error) {
	e.execMu.Lock()
	defer e.execMu.Unlock()
	return e.instantiateAndRunLocked(ctx, compiled)
}

func (e *engine) instantiateAndRunLocked(ctx context.Context, compiled wazero.CompiledModule) (any, error) {
	if e.runtime == nil {
		e.setLastError(ErrWazeroRuntimeNotInitialized)
		return nil, ErrWazeroRuntimeNotInitialized
	}

	// 如有旧实例则先关闭。
	if e.instance != nil {
		_ = e.instance.Close(context.Background())
		e.instance = nil
	}

	mod, err := e.runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig())
	if err != nil {
		wrapped := fmt.Errorf("%w: %v", ErrWazeroInstantiateFailed, err)
		e.setLastError(wrapped)
		return nil, wrapped
	}
	e.instance = mod

	if fn := mod.ExportedFunction("_start"); fn != nil {
		if _, err := fn.Call(ctx); err != nil {
			wrapped := fmt.Errorf("wazero engine: _start failed: %w", err)
			e.setLastError(wrapped)
			return nil, wrapped
		}
	}

	e.ClearError()
	return nil, nil
}

func (e *engine) rebuildHostModule() error {
	e.execMu.Lock()
	defer e.execMu.Unlock()

	if e.runtime == nil {
		return ErrWazeroRuntimeNotInitialized
	}

	// 关闭旧宿主模块。
	if e.hostModule != nil {
		_ = e.hostModule.Close(context.Background())
		e.hostModule = nil
	}

	if len(e.hostFunctions) == 0 {
		return nil
	}

	builder := e.runtime.NewHostModuleBuilder(hostModuleName)
	for name, fn := range e.hostFunctions {
		builder.NewFunctionBuilder().WithFunc(fn).Export(name)
	}

	mod, err := builder.Instantiate(context.Background())
	if err != nil {
		return fmt.Errorf("wazero engine: rebuild host module: %w", err)
	}
	e.hostModule = mod
	return nil
}

func toUint64(v any) uint64 {
	switch val := v.(type) {
	case uint64:
		return val
	case uint32:
		return uint64(val)
	case int:
		return uint64(val)
	case int32:
		return uint64(val)
	case int64:
		return uint64(val)
	case float64:
		return uint64(val)
	case float32:
		return uint64(val)
	default:
		return 0
	}
}
