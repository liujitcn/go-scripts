package yaegi

import (
	"context"
	"fmt"
)

////////////////////////////////////////////////////////////////////////////////
// 内部辅助函数。
////////////////////////////////////////////////////////////////////////////////

func (e *engine) eval(ctx context.Context, code string) (any, error) {

	if err := ctx.Err(); err != nil {
		e.setLastError(err)
		return nil, err
	}

	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.interp == nil {
		e.setLastError(ErrYaegiInterpreterNotInitialized)
		return nil, ErrYaegiInterpreterNotInitialized
	}

	var result any
	var evalErr error

	func() {
		defer func() {
			if r := recover(); r != nil {
				evalErr = fmt.Errorf("yaegi engine: panic during eval: %v", r)
			}
		}()

		v, err := e.interp.Eval(code)
		if err != nil {
			evalErr = fmt.Errorf("%w: %v", ErrYaegiEvalFailed, err)
			return
		}
		if v.IsValid() {
			result = v.Interface()
		}
	}()

	if evalErr != nil {
		e.setLastError(evalErr)
		return nil, evalErr
	}

	e.ClearError()
	return result, nil
}
