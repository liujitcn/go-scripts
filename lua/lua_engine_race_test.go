package lua

import (
	"context"
	"sync"
	"testing"
	"time"

	script_engine "github.com/liujitcn/go-scripts"
)

const busyLoopScript = "local s = 0 for i = 1, 20000000 do s = s + i end"

func TestLuaRace_ConcurrentExecuteString(t *testing.T) {
	e := newStartedEngine(t)
	defer func() { waitForClose(t, e) }()

	if _, err := e.ExecuteString(context.Background(), "reset", "counter = 0"); err != nil {
		t.Fatal(err)
	}

	const goroutines, runs = 8, 25

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*runs)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < runs; i++ {
				if _, err := e.ExecuteString(context.Background(), "inc", "counter = (counter or 0) + 1"); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent ExecuteString failed: %v", err)
	}

	v, err := e.GetGlobal("counter")
	if err != nil {
		t.Fatal(err)
	}
	if v != int64(goroutines*runs) {
		t.Fatalf("counter = %v, want %d — runs were not serialized", v, goroutines*runs)
	}
}

func TestLuaRace_ConcurrentCallFunction(t *testing.T) {
	e := newStartedEngine(t)
	defer func() { waitForClose(t, e) }()

	if _, err := e.ExecuteString(context.Background(), "def", "function add(a, b) return a + b end"); err != nil {
		t.Fatal(err)
	}

	const goroutines, calls = 8, 25

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*calls)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < calls; i++ {
				a := int64(id*calls + i)
				res, err := e.CallFunction(context.Background(), "add", a, a)
				if err != nil {
					errs <- err
					return
				}
				if res != 2*a {
					errs <- &unexpectedResultError{got: res, want: 2 * a}
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent CallFunction failed: %v", err)
	}
}

type unexpectedResultError struct {
	got  any
	want any
}

func (e *unexpectedResultError) Error() string {
	return "unexpected result"
}

func TestLuaRace_CancelWhileOthersUseEngine(t *testing.T) {
	e := newStartedEngine(t)
	defer func() { waitForClose(t, e) }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := e.ExecuteString(ctx, "runaway", runawayScript)
		done <- err
	}()

	time.Sleep(5 * time.Millisecond)
	if err := e.RegisterGlobal("raceVal", "survived"); err != nil {
		t.Fatalf("RegisterGlobal blocked/failed: %v", err)
	}
	if _, err := e.ExecuteString(context.Background(), "ok", "ok = 1"); err != nil {
		t.Fatalf("engine not usable while/after cancelled run: %v", err)
	}

	if err := <-done; err != context.DeadlineExceeded {
		t.Fatalf("runaway run returned %v, want context.DeadlineExceeded", err)
	}
	if v, err := e.GetGlobal("raceVal"); err != nil || v != "survived" {
		t.Fatalf("global written during cancel window lost: v=%v err=%v", v, err)
	}
}

func TestLuaRace_CloseConcurrentWithCancelledRun(t *testing.T) {
	e := newStartedEngine(t)

	runErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		_, err := e.ExecuteString(ctx, "runaway", runawayScript)
		runErr <- err
	}()

	time.Sleep(5 * time.Millisecond)
	waitForClose(t, e) // must not hang behind the leaked... er, running goroutine

	select {
	case err := <-runErr:
		switch err {
		case context.DeadlineExceeded, context.Canceled, ErrLuaEngineNotInitialized:
		default:
			t.Fatalf("unexpected run error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled run never returned after Close")
	}
}

func TestLuaRace_MixedSyncAsyncAndQuota(t *testing.T) {
	e := newStartedEngine(t)
	defer func() { waitForClose(t, e) }()

	e.SetQuota(script_engine.Quota{Timeout: 2 * time.Second})

	if err := e.LoadString(context.Background(), "base", "baseLoaded = true"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ExecuteString(context.Background(), "def", "function mul(a, b) return a * b end"); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	fail := func(op string, err error) { errs <- &opError{op: op, err: err} }

	wg.Add(1)
	go func() { // ExecuteSync under quota
		defer wg.Done()
		for i := 0; i < 10; i++ {
			if _, err := e.ExecuteSync(context.Background()); err != nil {
				fail("ExecuteSync", err)
				return
			}
		}
	}()
	wg.Add(1)
	go func() { // Execute (LoadString + run)
		defer wg.Done()
		for i := 0; i < 10; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := e.LoadString(ctx, "m", "raceN = (raceN or 0) + 1")
			if err == nil {
				_, err = e.Execute(ctx)
			}
			cancel()
			if err != nil {
				fail("LoadString/Execute", err)
				return
			}
		}
	}()
	wg.Add(1)
	go func() { // cancelled-context ExecuteString on a FINITE script (completes before deadline)
		defer wg.Done()
		for i := 0; i < 10; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if _, err := e.ExecuteString(ctx, "finite", "local s = 0 for i = 1, 1000 do s = s + i end"); err != nil {
				cancel()
				fail("ExecuteString(finite)", err)
				return
			}
			cancel()
		}
	}()
	wg.Add(1)
	go func() { // CallFunction
		defer wg.Done()
		for i := 1; i <= 10; i++ {
			res, err := e.CallFunction(context.Background(), "mul", int64(i), int64(3))
			if err != nil {
				fail("CallFunction", err)
				return
			}
			if res != int64(3*i) {
				fail("CallFunction", &unexpectedResultError{got: res, want: int64(3 * i)})
				return
			}
		}
	}()

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("mixed concurrent operations failed: %v", err)
	}
}

type opError struct {
	op  string
	err error
}

func (e *opError) Error() string {
	return e.op + ": " + e.err.Error()
}

func TestLuaRace_EngineChurnCrossPool(t *testing.T) {
	const workers, iterations = 4, 3

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				e, err := newLuaEngine()
				if err != nil {
					errs <- err
					return
				}
				if err := e.Init(context.Background()); err != nil {
					errs <- err
					return
				}
				if _, err := e.ExecuteString(context.Background(), "ok", "ok = 1"); err != nil {
					errs <- err
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				_, err = e.ExecuteString(ctx, "runaway", runawayScript)
				cancel()
				if err != context.DeadlineExceeded {
					errs <- err
					return
				}
				closed := make(chan error, 1)
				go func() { closed <- e.Close() }()
				select {
				case err := <-closed:
					if err != nil {
						errs <- err
						return
					}
				case <-time.After(5 * time.Second):
					errs <- context.DeadlineExceeded // Close wedged: e.mu leaked
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("engine churn failed: %v", err)
	}
}

func TestLuaRace_ConcurrentLoadExecute(t *testing.T) {
	e := newStartedEngine(t)
	defer func() { waitForClose(t, e) }()

	scripts := []string{"loadedA = 1", "loadedB = 2"}

	if err := e.LoadString(context.Background(), "swap", scripts[0]); err != nil {
		t.Fatal(err)
	}

	var executors sync.WaitGroup
	errs := make(chan error, 64)
	stop := make(chan struct{})

	var loader sync.WaitGroup
	loader.Add(1)
	go func() { // rotating loader
		defer loader.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := e.LoadString(context.Background(), "swap", scripts[i%len(scripts)]); err != nil {
				errs <- err
				return
			}
		}
	}()
	for g := 0; g < 4; g++ {
		executors.Add(1)
		go func() {
			defer executors.Done()
			for i := 0; i < 50; i++ {
				if _, err := e.Execute(context.Background()); err != nil {
					errs <- err
					return
				}
			}
		}()
	}

	finished := make(chan struct{})
	go func() { executors.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		close(stop)
		t.Fatal("concurrent load/execute wedged")
		return
	}
	close(stop)
	loader.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent load/execute failed: %v", err)
	}
}

func TestLuaRace_SetQuotaConcurrentWithRuns(t *testing.T) {
	e := newStartedEngine(t)
	defer func() { waitForClose(t, e) }()

	stop := make(chan struct{})
	var togglers sync.WaitGroup
	for i := 0; i < 2; i++ {
		togglers.Add(1)
		go func() {
			defer togglers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					e.SetQuota(script_engine.Quota{Timeout: 5 * time.Second})
					e.SetQuota(script_engine.Quota{})
				}
			}
		}()
	}

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				err := e.LoadString(context.Background(), "m", "local s = 0 for i = 1, 100 do s = s + i end")
				if err != nil {
					errs <- err
					return
				}
				if _, err := e.ExecuteSync(context.Background()); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	togglers.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("ExecuteSync under quota churn failed: %v", err)
	}
}
