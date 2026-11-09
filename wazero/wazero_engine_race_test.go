package wazero

import (
	"context"
	"sync"
	"testing"
)

// TestWazeroRace_LifecycleAndCancel 验证并发执行、取消及生命周期操作不会破坏 WebAssembly 引擎状态。
func TestWazeroRace_LifecycleAndCancel(t *testing.T) {
	eng := newTestEngine(t)
	t.Cleanup(func() { _ = eng.Close() })

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	var wg sync.WaitGroup
	errs := make(chan error, 64)

	for g := range 3 { // lifecycle churn with exact results
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range 5 {
				if err := eng.LoadString(context.Background(), "add", string(addWasm)); err != nil {
					errs <- err
					return
				}
				if _, err := eng.Execute(context.Background()); err != nil {
					errs <- err
					return
				}
				x := uint64(id*10 + i)
				res, err := eng.CallFunction(context.Background(), "add", x, uint64(4))
				if err != nil {
					errs <- err
					return
				}
				if res != x+4 {
					errs <- &wazeroResultError{got: res}
					return
				}
			}
		}(g)
	}
	for range 2 { // cancelled-ctx calls churn ctx plumbing; results
		wg.Go(func() {
			for range 5 {
				_ = eng.LoadString(cancelled, "add", string(addWasm))
				_, _ = eng.CallFunction(cancelled, "add", uint64(1), uint64(2))
			}
		})
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("lifecycle/cancel churn failed: %v", err)
	}

	if err := eng.LoadString(context.Background(), "add", string(addWasm)); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := eng.CallFunction(context.Background(), "add", uint64(3), uint64(4))
	if err != nil {
		t.Fatal(err)
	}
	if res != uint64(7) {
		t.Fatalf("final call returned %v, want 7", res)
	}
}

type wazeroResultError struct {
	got any
}

func (e *wazeroResultError) Error() string {
	return "unexpected result"
}
