package lua

import (
	"context"
	"testing"
	"time"
)

const runawayScript = "while true do end"

func waitForClose(t *testing.T, e *engine) {
	t.Helper()
	closed := make(chan error, 1)
	go func() { closed <- e.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return within 5s — run goroutine still holds e.mu (leak/deadlock regression)")
	}
}

func newStartedEngine(t *testing.T) *engine {
	t.Helper()
	e, err := newLuaEngine()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestLuaAsyncCancel_ExecuteString(t *testing.T) {
	for i := 0; i < 2; i++ {
		e := newStartedEngine(t)

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		_, err := e.ExecuteString(ctx, "spin", runawayScript)
		cancel()
		if err != context.DeadlineExceeded {
			t.Fatalf("iter %d: expected context.DeadlineExceeded, got %v", i, err)
		}

		if _, err := e.ExecuteString(context.Background(), "ok", "ok = 1"); err != nil {
			t.Fatalf("iter %d: engine not reusable after cancelled run: %v", i, err)
		}
		if v, err := e.GetGlobal("ok"); err != nil || v != int64(1) {
			t.Fatalf("iter %d: unexpected global after reuse: v=%v err=%v", i, v, err)
		}

		waitForClose(t, e)
	}
}

func TestLuaAsyncCancel_Execute(t *testing.T) {
	e := newStartedEngine(t)
	defer func() { waitForClose(t, e) }()

	if err := e.LoadString(context.Background(), "spin", runawayScript); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := e.Execute(ctx); err != context.DeadlineExceeded {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestLuaAsyncCancel_CallFunction(t *testing.T) {
	e := newStartedEngine(t)
	defer func() { waitForClose(t, e) }()

	if _, err := e.ExecuteString(context.Background(), "def", "function spin() while true do end end"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := e.CallFunction(ctx, "spin"); err != context.DeadlineExceeded {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestLuaAsyncCancel_AlreadyCancelledContext(t *testing.T) {
	e := newStartedEngine(t)
	defer func() { waitForClose(t, e) }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := e.ExecuteString(ctx, "spin", runawayScript); err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if _, err := e.ExecuteString(context.Background(), "ok", "ok = 1"); err != nil {
		t.Fatalf("engine not reusable after cancelled run: %v", err)
	}
}

func TestLuaAsyncCancel_DeadlineLessContextKeepsFastPath(t *testing.T) {
	e := newStartedEngine(t)
	defer func() { waitForClose(t, e) }()

	if _, err := e.ExecuteString(context.Background(), "ok", "ok = 42"); err != nil {
		t.Fatalf("background-context run failed: %v", err)
	}
	if v, err := e.GetGlobal("ok"); err != nil || v != int64(42) {
		t.Fatalf("unexpected global: v=%v err=%v", v, err)
	}
}
