package yaegi

import (
	"context"
	"sync"
	"testing"
)

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

	for g := 0; g < 3; g++ { // exact-value runs
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
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
		}()
	}
	for g := 0; g < 3; g++ { // scripted-function calls
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
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
	for g := 0; g < 2; g++ { // pre-cancelled contexts fail fast, engine unharmed
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if _, err := eng.ExecuteString(cancelled, "never", "1 + 2"); err == nil {
					errs <- context.Canceled
					return
				}
			}
		}()
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
