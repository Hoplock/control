// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package action_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/action"
)

// fakeHandler is a host's ext.ActionHandler that does what a test says.
type fakeHandler struct {
	startErr error
	panicOn  bool
	block    chan struct{} // Start waits on it when set
	stopHang bool          // Stop waits for its context

	starts, stops atomic.Int32

	mu   sync.Mutex
	exec ext.ActionExecutor
	ctx  context.Context
}

func (f *fakeHandler) Start(ctx context.Context, exec ext.ActionExecutor) error {
	f.starts.Add(1)
	f.mu.Lock()
	f.exec, f.ctx = exec, ctx
	f.mu.Unlock()
	if f.panicOn {
		panic("the handler's Start exploded")
	}
	if f.block != nil {
		<-f.block
	}
	return f.startErr
}

func (f *fakeHandler) Stop(ctx context.Context) error {
	f.stops.Add(1)
	if f.stopHang {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (f *fakeHandler) executor() ext.ActionExecutor {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exec
}

// A handler is started once, with the server's lifetime as its context and a
// working executor, and stopped on shutdown.
func TestAHandlerIsStartedOnceWithAWorkingExecutorAndStoppedOnShutdown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fake := &fakeHandler{}
	runner := action.NewRunner(fake, h.exec, slog.New(slog.DiscardHandler))

	lifetime, cancel := context.WithCancel(t.Context())
	defer cancel()
	if started, _ := runner.Started(); started {
		t.Fatal("a handler that was never started reports started")
	}
	runner.Start(lifetime)
	if started, kind := runner.Started(); !started || kind != "" {
		t.Fatalf("Started = %v, %q after a clean Start", started, kind)
	}
	if fake.starts.Load() != 1 {
		t.Errorf("Start was called %d times", fake.starts.Load())
	}
	fake.mu.Lock()
	ctx := fake.ctx
	fake.mu.Unlock()
	if ctx != lifetime {
		t.Error("the handler was not started under the server's lifetime")
	}

	// The executor it was handed works.
	res, err := fake.executor().Execute(t.Context(), ext.Action{
		Tenant: ext.Tenant(tenant), Kind: ext.ActionLockSubject, ActionID: "h-1", SubjectID: alice,
	})
	if err != nil || res.Affected != 1 {
		t.Fatalf("the handler's executor = %+v, %v", res, err)
	}

	if err := runner.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := runner.Stop(t.Context()); err != nil {
		t.Fatalf("a second Stop: %v", err)
	}
	if fake.stops.Load() != 1 {
		t.Errorf("Stop was called %d times, want once", fake.stops.Load())
	}
	// An Execute after Stop began is KindUnavailable, through the executor the
	// handler still holds.
	if _, err := fake.executor().Execute(t.Context(), ext.Action{
		Tenant: ext.Tenant(tenant), Kind: ext.ActionLockSubject, ActionID: "h-2", SubjectID: bob,
	}); ext.KindOf(err) != ext.KindUnavailable {
		t.Errorf("Execute after Stop: %v, want KindUnavailable", err)
	}
	if started, _ := runner.Started(); started {
		t.Error("a stopped handler reports started")
	}
}

// A Start that fails does not stop Control: the failure is reported with its
// kind, nothing is executed through a handler that is not running, and Stop is
// still safe to call.
func TestAHandlerWhoseStartFailsIsRegisteredAndNotStarted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	for name, c := range map[string]struct {
		handler *fakeHandler
		kind    string
	}{
		"a classified error":    {&fakeHandler{startErr: ext.Errorf(ext.PointActionHandler, "x", "Start", ext.KindInvalid, "bad config")}, "invalid"},
		"an unclassified one":   {&fakeHandler{startErr: errors.New("boom")}, "internal"},
		"an unreachable broker": {&fakeHandler{startErr: ext.Errorf(ext.PointActionHandler, "x", "Start", ext.KindUnavailable, "no route")}, "unavailable"},
		"a panic":               {&fakeHandler{panicOn: true}, "internal"},
	} {
		t.Run(name, func(t *testing.T) {
			runner := action.NewRunner(c.handler, h.exec, slog.New(slog.DiscardHandler))
			runner.Start(t.Context())
			started, kind := runner.Started()
			if started || kind != c.kind {
				t.Errorf("Started = %v, %q; want not started with kind %q", started, kind, c.kind)
			}
			if err := runner.Stop(t.Context()); err != nil {
				t.Errorf("Stop after a failed Start: %v", err)
			}
			if c.handler.stops.Load() != 1 {
				t.Errorf("Stop reached the handler %d times, want once: it is safe without a Start", c.handler.stops.Load())
			}
		})
	}
}

// A Start that never returns is a broken registration too: Control stops
// waiting for it.
func TestAHandlerThatNeverReturnsFromStartDoesNotHoldControlUp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fake := &fakeHandler{block: make(chan struct{})}
	runner := action.NewRunner(fake, h.exec, slog.New(slog.DiscardHandler))
	action.SetStartTimeout(runner, 50*time.Millisecond)

	begun := time.Now()
	runner.Start(t.Context())
	if time.Since(begun) > 5*time.Second {
		t.Fatal("Start waited for a handler that never returned")
	}
	if started, kind := runner.Started(); started || kind != "unavailable" {
		t.Errorf("Started = %v, %q; want not started, unavailable", started, kind)
	}

	// If it does return later, the truth is what it returned.
	close(fake.block)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if started, _ := runner.Started(); started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a handler that returned late was never reported started")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Shutdown is Control's: a handler whose Stop hangs is abandoned at the
// deadline.
func TestAHandlerThatHangsInStopIsAbandonedAtTheDeadline(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fake := &fakeHandler{stopHang: true}
	runner := action.NewRunner(fake, h.exec, slog.New(slog.DiscardHandler))
	runner.Start(t.Context())

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	begun := time.Now()
	err := runner.Stop(ctx)
	if err == nil || time.Since(begun) > 5*time.Second {
		t.Errorf("Stop = %v after %s, want an error at the deadline", err, time.Since(begun))
	}
	if _, execErr := fake.executor().Execute(t.Context(), ext.Action{
		Tenant: ext.Tenant(tenant), Kind: ext.ActionLockSubject, ActionID: "x", SubjectID: alice,
	}); ext.KindOf(execErr) != ext.KindUnavailable {
		t.Errorf("Execute after an abandoned Stop: %v, want KindUnavailable", execErr)
	}
}
