package tcl

import (
	"context"
	"sync"
	"testing"
)

// TestTCLRace_ExecuteAndCallMixed 验证 Tcl 执行、宿主函数调用与变量注册可并发完成。
func TestTCLRace_ExecuteAndCallMixed(t *testing.T) {
	eng := getSharedEngine(t)

	if _, err := eng.ExecuteString(context.Background(), "def", `
proc add {a b} {
    return [expr {$a + $b}]
}
`); err != nil {
		t.Fatal(err)
	}

	const goroutines, runs = 4, 10

	var wg sync.WaitGroup
	errs := make(chan error, 64)

	for range goroutines { // exact-value expression runs
		wg.Go(func() {
			for range runs {
				res, err := eng.ExecuteString(context.Background(), "expr", "expr {1 + 2}")
				if err != nil {
					errs <- err
					return
				}
				if res != int64(3) {
					errs <- &tclResultError{op: "ExecuteString", got: res}
					return
				}
			}
		})
	}
	for g := range goroutines { // proc calls
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range runs {
				a := int64(id*runs + i)
				res, err := eng.CallFunction(context.Background(), "add", a, a)
				if err != nil {
					errs <- err
					return
				}
				if res != 2*a {
					errs <- &tclResultError{op: "CallFunction", got: res}
					return
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("mixed concurrent operations failed: %v", err)
	}
}

type tclResultError struct {
	op  string
	got any
}

func (e *tclResultError) Error() string {
	return e.op + ": unexpected result"
}
