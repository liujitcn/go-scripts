package starlark

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestStarlarkRace_ExecuteAndCallMixed(t *testing.T) {
	eng, err := newStarlarkEngine()
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	if _, err := eng.ExecuteString(context.Background(), "def", "def add(a, b):\n    return a + b\n"); err != nil {
		t.Fatal(err)
	}

	const goroutines, runs = 5, 10

	var wg sync.WaitGroup
	errs := make(chan error, 64)

	for g := 0; g < goroutines; g++ { // per-run distinct globals
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < runs; i++ {
				script := fmt.Sprintf("g_%d_%d = %q\n", id, i, fmt.Sprintf("v%d_%d", id, i))
				if _, err := eng.ExecuteString(context.Background(), "set", script); err != nil {
					errs <- err
					return
				}
			}
		}(g)
	}
	for g := 0; g < goroutines; g++ { // scripted-function calls
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < runs; i++ {
				a := int64(id*runs + i)
				res, err := eng.CallFunction(context.Background(), "add", a, a)
				if err != nil {
					errs <- err
					return
				}
				if res != 2*a {
					errs <- &starlarkResultError{op: "CallFunction", got: res}
					return
				}
			}
		}(g)
	}
	wg.Add(1)
	go func() { // global churn while the others run
		defer wg.Done()
		for i := 0; i < runs; i++ {
			if err := eng.RegisterGlobal("churnKey", i); err != nil {
				errs <- err
				return
			}
		}
	}()

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("mixed concurrent operations failed: %v", err)
	}

	for _, key := range []string{"g_0_0", "g_2_5", "g_4_9"} {
		want := "v" + key[2:]
		v, err := eng.GetGlobal(key)
		if err != nil {
			t.Fatalf("global %s lost: %v", key, err)
		}
		if v != want {
			t.Fatalf("global %s = %v, want %q", key, v, want)
		}
	}
}

type starlarkResultError struct {
	op  string
	got any
}

func (e *starlarkResultError) Error() string {
	return e.op + ": unexpected result"
}
