package yaegi

import (
	"context"
	"sync"
	"testing"
)

// TestYaegiRace_ExecuteAndCallMixed 验证 Go 脚本执行、函数调用及取消请求可并发完成。
func TestYaegiRace_ExecuteAndCallMixed(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()

	if _, err := eng.ExecuteString(context.Background(), "def", `func add(a, b int) int { return a + b }`); err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	var wg sync.WaitGroup
	errs := make(chan error, 64)

	for range 3 { // exact-value runs
		wg.Go(func() {
			for range 10 {
				res, err := eng.ExecuteString(context.Background(), "expr", "1 + 2")
				if err != nil {
					errs <- err
					return
				}
				if res != 3 {
					errs <- &yaegiResultError{op: "ExecuteString", got: res}
					return
				}
			}
		})
	}
	for g := range 3 { // scripted-function calls
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range 10 {
				a := id*10 + i
				res, err := eng.CallFunction(context.Background(), "add", a, a)
				if err != nil {
					errs <- err
					return
				}
				if res != 2*a {
					errs <- &yaegiResultError{op: "CallFunction", got: res}
					return
				}
			}
		}(g)
	}
	for range 2 { // pre-cancelled contexts fail fast, engine unharmed
		wg.Go(func() {
			for range 10 {
				if _, err := eng.ExecuteString(cancelled, "never", "1 + 2"); err == nil {
					errs <- context.Canceled
					return
				}
			}
		})
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("mixed concurrent operations failed: %v", err)
	}
}

type yaegiResultError struct {
	op  string
	got any
}

func (e *yaegiResultError) Error() string {
	return e.op + ": unexpected result"
}
