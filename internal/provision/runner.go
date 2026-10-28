// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package provision

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hoplock/control/ext"
)

// DefaultStartTimeout is how long Control waits for a sync's Start to return.
// ext.IdentitySync tells a sync to return promptly and start its own
// goroutines, so a Start that has not returned by now is a broken registration
// and not a slow directory: making Control's own start-up wait on somebody
// else's software would be the outage M11 exists to prevent. It is the action
// handler's bound (0039), for the same reason.
const DefaultStartTimeout = 30 * time.Second

// Runner starts and stops the one registered ext.IdentitySync, and says whether
// it is running. A sync whose Start fails does not stop Control: the failure is
// logged at ERROR with the provider and its kind, and [Runner.Started] says the
// sync is registered and not started. The sink stays usable: a sync that
// failed to Start but calls it anyway is applied, because what the directory
// says is true whether or not the sync's own start-up went well.
type Runner struct {
	sync     ext.IdentitySync
	sink     *Sink
	provider string
	timeout  time.Duration
	log      *slog.Logger

	mu       sync.Mutex
	started  bool
	failure  string
	stopOnce sync.Once
	stopErr  error
}

// NewRunner builds the lifecycle for a registered sync and the sink it applies
// what it learns through.
func NewRunner(s ext.IdentitySync, sink *Sink, log *slog.Logger) *Runner {
	if log == nil {
		log = slog.Default()
	}
	return &Runner{sync: s, sink: sink, provider: sink.provider, timeout: DefaultStartTimeout, log: log}
}

// Provider is the registered sync's provider.
func (r *Runner) Provider() string { return r.provider }

// Started reports whether the sync's Start returned without an error, and when
// it did not, the kind of the error (`internal` for one that is not
// classified). A sync that has not been started yet is not started.
func (r *Runner) Started() (started bool, errorKind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started, r.failure
}

// Start calls the sync's Start once, with ctx as the context the sync runs
// under (the server's lifetime) and the sink as the only thing it can apply
// through. It never returns an error: a sync that fails to start is reported,
// not fatal.
func (r *Runner) Start(ctx context.Context) {
	done := make(chan error, 1)
	go func() {
		defer func() {
			// A sync that panics in Start is a broken registration too.
			if p := recover(); p != nil {
				done <- ext.Errorf(ext.PointIdentitySync, r.provider, "IdentitySync.Start", ext.KindInternal, "it panicked: %v", p)
			}
		}()
		done <- r.sync.Start(ctx, r.sink)
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
	r.log.InfoContext(ctx, "the identity sync started",
		"event", "identity_sync_started", "provider", r.provider)
}

func (r *Runner) fail(ctx context.Context, kind ext.Kind, err error) {
	r.mu.Lock()
	r.started, r.failure = false, kind.String()
	r.mu.Unlock()
	r.log.ErrorContext(ctx, "the identity sync failed to start; Control serves, and identities arrive at login",
		"event", "identity_sync_not_started", "provider", r.provider, "kind", kind.String(), "error", err.Error())
}

// Stop ends the sync. Every sink call from now on is KindUnavailable, even one
// racing the sync's own Stop, and the sync is stopped whether or not Start
// succeeded, as ext.IdentitySync promises it can be. It returns when the sync's
// Stop does, or when ctx ends, whichever is first: the shutdown deadline is
// Control's, not the sync's.
func (r *Runner) Stop(ctx context.Context) error {
	r.stopOnce.Do(func() {
		r.sink.stopping()
		done := make(chan error, 1)
		go func() {
			defer func() {
				if p := recover(); p != nil {
					done <- fmt.Errorf("the sync panicked in Stop: %v", p)
				}
			}()
			done <- r.sync.Stop(ctx)
		}()
		select {
		case r.stopErr = <-done:
		case <-ctx.Done():
			r.stopErr = fmt.Errorf("the sync did not stop inside the shutdown deadline: %w", ctx.Err())
		}
		r.mu.Lock()
		r.started = false
		r.mu.Unlock()
	})
	return r.stopErr
}
