// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package action

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hoplock/control/ext"
)

// DefaultStartTimeout is how long Control waits for a handler's Start to
// return. ext.ActionHandler tells a handler to return promptly and start its
// own goroutines, so a Start that has not returned by now is a broken
// registration and not a slow broker: making Control's own start-up wait on
// somebody else's automation would be the outage M11 exists to prevent.
const DefaultStartTimeout = 30 * time.Second

// Runner starts and stops the one registered ext.ActionHandler, and says
// whether it is running. A handler whose Start fails does not stop Control: the
// failure is logged at ERROR with the provider and its kind, nothing is
// executed, and [Runner.Started] says the handler is registered and not
// started.
type Runner struct {
	handler  ext.ActionHandler
	exec     *Executor
	provider string
	timeout  time.Duration
	log      *slog.Logger

	mu       sync.Mutex
	started  bool
	failure  string
	stopOnce sync.Once
	stopErr  error
}

// NewRunner builds the lifecycle for a registered handler and the executor it
// acts through.
func NewRunner(handler ext.ActionHandler, exec *Executor, log *slog.Logger) *Runner {
	if log == nil {
		log = slog.Default()
	}
	return &Runner{handler: handler, exec: exec, provider: exec.provider, timeout: DefaultStartTimeout, log: log}
}

// Provider is the registered handler's provider.
func (r *Runner) Provider() string { return r.provider }

// Started reports whether the handler's Start returned without an error, and
// when it did not, the kind of the error (`internal` for one that is not
// classified, which is what an unclassified error is). A handler that has not
// been started yet is not started.
func (r *Runner) Started() (started bool, errorKind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started, r.failure
}

// Start calls the handler's Start once, with ctx as the context the handler
// runs under (the server's lifetime) and the executor as the only thing it can
// act through. It never returns an error: a handler that fails to start is
// reported, not fatal.
func (r *Runner) Start(ctx context.Context) {
	done := make(chan error, 1)
	go func() {
		defer func() {
			// A handler that panics in Start is a broken registration too.
			if p := recover(); p != nil {
				done <- ext.Errorf(ext.PointActionHandler, r.provider, "ActionHandler.Start", ext.KindInternal, "it panicked: %v", p)
			}
		}()
		done <- r.handler.Start(ctx, r.exec)
	}()

	timer := time.NewTimer(r.timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		r.finish(ctx, err)
	case <-timer.C:
		r.fail(ctx, ext.KindUnavailable, fmt.Errorf("Start did not return within %s", r.timeout))
		// If it returns later, the truth is whatever it returns.
		go func() { r.finish(ctx, <-done) }()
	case <-ctx.Done():
		r.fail(ctx, ext.KindUnavailable, ctx.Err())
	}
}

func (r *Runner) finish(ctx context.Context, err error) {
	if err != nil {
		r.fail(ctx, ext.KindOf(err), err)
		return
	}
	r.mu.Lock()
	r.started, r.failure = true, ""
	r.mu.Unlock()
	r.log.InfoContext(ctx, "the inbound action handler started",
		"event", "action_handler_started", "provider", r.provider)
}

func (r *Runner) fail(ctx context.Context, kind ext.Kind, err error) {
	r.mu.Lock()
	r.started, r.failure = false, kind.String()
	r.mu.Unlock()
	r.log.ErrorContext(ctx, "the inbound action handler failed to start; Control serves, and nothing will be executed",
		"event", "action_handler_not_started", "provider", r.provider, "kind", kind.String(), "error", err.Error())
}

// Stop ends the handler. Every Execute from now on is KindUnavailable, even
// one that is racing the handler's own Stop, and the handler is stopped whether
// or not Start succeeded, as ext.ActionHandler promises it can be. It returns
// when the handler's Stop does, or when ctx ends, whichever is first: the
// shutdown deadline is Control's, not the handler's.
func (r *Runner) Stop(ctx context.Context) error {
	r.stopOnce.Do(func() {
		r.exec.stopping()
		done := make(chan error, 1)
		go func() {
			defer func() {
				if p := recover(); p != nil {
					done <- fmt.Errorf("the handler panicked in Stop: %v", p)
				}
			}()
			done <- r.handler.Stop(ctx)
		}()
		select {
		case r.stopErr = <-done:
		case <-ctx.Done():
			r.stopErr = fmt.Errorf("the handler did not stop inside the shutdown deadline: %w", ctx.Err())
		}
		r.mu.Lock()
		r.started = false
		r.mu.Unlock()
	})
	return r.stopErr
}
