package expr

import (
	"context"
	"sync"
	"testing"
)

// TestExprRace_ExecuteAndCallMixed 验证表达式执行、宿主函数调用与全局变量注册可并发完成。
func TestExprRace_ExecuteAndCallMixed(t *testing.T) {
	eng, err := newExprEngine()
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	if err := eng.RegisterFunction("triple", func(x int) int { return x * 3 }); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 64)

	for range 3 { // exact-value expression runs
		wg.Go(func() {
			for range 20 {
				res, err := eng.ExecuteString(context.Background(), "expr", "1 + 2")
				if err != nil {
					errs <- err
					return
				}
				if res != 3 {
					errs <- &exprResultError{op: "ExecuteString", got: res}
					return
				}
			}
		})
	}
	for g := range 3 { // host-function calls
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range 20 {
				x := id*100 + i
				res, err := eng.CallFunction(context.Background(), "triple", x)
				if err != nil {
					errs <- err
					return
				}
				if res != 3*x {
					errs <- &exprResultError{op: "CallFunction", got: res}
					return
				}
			}
		}(g)
	}
	wg.Go(func() { // registration churn while the others run
		for i := range 20 {
			if err := eng.RegisterGlobal("churnKey", i); err != nil {
				errs <- err
				return
			}
		}
	})

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("mixed concurrent operations failed: %v", err)
	}
}

type exprResultError struct {
	op  string
	got any
}

func (e *exprResultError) Error() string {
	return e.op + ": unexpected result"
}
