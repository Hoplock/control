// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hoplock/control/ext"
)

// Defaults for a Dispatcher.
const (
	// DefaultQueue is how many notifications one destination may have
	// waiting. Past it a notification is dropped and the drop is logged:
	// a destination that is down must not make this process grow without
	// bound, and it must never make the caller wait.
	DefaultQueue = 256
	// DefaultAttempts is how many times a delivery is tried.
	DefaultAttempts = 5
	// DefaultBackoff is the first wait between attempts; it doubles, up to
	// maxBackoff.
	DefaultBackoff = time.Second
	// DefaultDeliveryTimeout bounds one attempt at a registered notifier.
	// The webhook has its own (notify.webhook_timeout).
	DefaultDeliveryTimeout = 5 * time.Second
	maxBackoff             = 30 * time.Second
)

// Delivery is one notification on its way to one destination. The id is minted
// once, at Notify, so every attempt carries the same one and a receiver can
// de-duplicate retries.
type Delivery struct {
	ID           string
	Notification ext.Notification
}

// Destination is somewhere notifications go.
type Destination interface {
	// Name identifies it in logs: `webhook`, or the provider a notifier
	// was registered under.
	Name() string
	// Deliver makes one attempt. An error wrapping ErrPermanent is not
	// retried: the destination answered, and asking again would get the
	// same answer.
	Deliver(ctx context.Context, d Delivery) error
}

// ErrPermanent marks a delivery failure that retrying cannot fix.
var ErrPermanent = errors.New("notify: the destination refused the notification")

// Dispatcher fans each notification out to every destination.
type Dispatcher struct {
	sinks []*sink
	log   *slog.Logger

	startOnce sync.Once
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// Options configures a Dispatcher.
type Options struct {
	// Webhook is the deployment's own webhook, nil when none is configured.
	Webhook *Webhook
	// Notifiers are the registered ext.Notifier implementations.
	Notifiers []ext.Bound[ext.Notifier]
	// Queue, Attempts, Backoff and DeliveryTimeout override the defaults.
	Queue           int
	Attempts        int
	Backoff         time.Duration
	DeliveryTimeout time.Duration
	Logger          *slog.Logger
}

// New builds a dispatcher. It delivers nothing until Start.
func New(o Options) *Dispatcher {
	if o.Queue <= 0 {
		o.Queue = DefaultQueue
	}
	if o.Attempts <= 0 {
		o.Attempts = DefaultAttempts
	}
	if o.Backoff <= 0 {
		o.Backoff = DefaultBackoff
	}
	if o.DeliveryTimeout <= 0 {
		o.DeliveryTimeout = DefaultDeliveryTimeout
	}
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}

	d := &Dispatcher{log: log}
	var dests []Destination
	if o.Webhook != nil {
		dests = append(dests, o.Webhook)
	}
	for _, b := range o.Notifiers {
		dests = append(dests, &registered{
			name:    b.Registration.Provider,
			impl:    b.Impl,
			timeout: o.DeliveryTimeout,
		})
	}
	for _, dest := range dests {
		d.sinks = append(d.sinks, &sink{
			dest:     dest,
			queue:    make(chan Delivery, o.Queue),
			attempts: o.Attempts,
			backoff:  o.Backoff,
			log:      log,
		})
	}
	return d
}

// Destinations names where notifications go, for the start-up log.
func (d *Dispatcher) Destinations() []string {
	out := make([]string, 0, len(d.sinks))
	for _, s := range d.sinks {
		out = append(out, s.dest.Name())
	}
	return out
}

// Start runs one worker per destination until Close.
func (d *Dispatcher) Start() {
	d.startOnce.Do(func() {
		for _, s := range d.sinks {
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				s.run()
			}()
		}
	})
}

// Notify queues a notification for every destination and returns at once.
//
// It never blocks and never fails: a full queue drops the notification for
// that destination and logs the drop, because the act that produced it has
// already happened and must not wait on news about itself.
func (d *Dispatcher) Notify(_ context.Context, n ext.Notification) {
	if len(d.sinks) == 0 {
		return
	}
	del := Delivery{ID: newDeliveryID(), Notification: n}
	for _, s := range d.sinks {
		s.offer(del)
	}
}

// Close stops accepting and drains what is queued, until ctx is done.
func (d *Dispatcher) Close(ctx context.Context) error {
	d.closeOnce.Do(func() {
		for _, s := range d.sinks {
			s.close()
		}
	})
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		for _, s := range d.sinks {
			s.abandon()
		}
		return fmt.Errorf("notify: queued notifications were not all delivered before shutdown: %w", ctx.Err())
	}
}

// sink is one destination's queue and worker.
type sink struct {
	dest     Destination
	queue    chan Delivery
	attempts int
	backoff  time.Duration
	log      *slog.Logger

	mu     sync.Mutex
	closed bool
	// stop is closed to abandon retries at a shutdown deadline.
	stopOnce sync.Once
	stop     chan struct{}
}

func (s *sink) stopCh() chan struct{} {
	s.stopOnce.Do(func() { s.stop = make(chan struct{}) })
	return s.stop
}

func (s *sink) offer(d Delivery) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.queue <- d:
	default:
		s.log.Warn("a notification was dropped because its destination is not keeping up",
			"event", "notification_dropped",
			"destination", s.dest.Name(),
			"kind", d.Notification.Kind,
			"tenant", string(d.Notification.Tenant),
			"delivery_id", d.ID,
		)
	}
}

func (s *sink) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.queue)
	}
}

func (s *sink) abandon() { close(s.stopCh()) }

func (s *sink) run() {
	stop := s.stopCh()
	for d := range s.queue {
		select {
		case <-stop:
			// Shutdown's deadline passed: what is still queued is logged
			// by nobody and delivered by nobody, which Close reported.
			return
		default:
		}
		s.deliver(d, stop)
	}
}

// deliver tries one notification until it lands, is refused, or runs out of
// attempts.
func (s *sink) deliver(d Delivery, stop <-chan struct{}) {
	wait := s.backoff
	for attempt := 1; ; attempt++ {
		err := s.dest.Deliver(context.Background(), d)
		if err == nil {
			return
		}
		final := errors.Is(err, ErrPermanent) || attempt >= s.attempts
		attrs := []any{
			"event", "notification_failed",
			"destination", s.dest.Name(),
			"kind", d.Notification.Kind,
			"tenant", string(d.Notification.Tenant),
			"delivery_id", d.ID,
			"attempt", attempt,
			"error", err.Error(),
		}
		if final {
			s.log.Error("a notification could not be delivered", attrs...)
			return
		}
		s.log.Warn("a notification delivery failed and will be retried", attrs...)
		select {
		case <-time.After(wait):
		case <-stop:
			return
		}
		wait = min(wait*2, maxBackoff)
	}
}

// registered adapts an ext.Notifier. Its call is bounded, because the seam's
// contract says Notify must not block on a human and this is where that is
// enforced rather than trusted.
type registered struct {
	name    string
	impl    ext.Notifier
	timeout time.Duration
}

func (r *registered) Name() string { return r.name }

func (r *registered) Deliver(ctx context.Context, d Delivery) error {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	err := r.impl.Notify(ctx, d.Notification)
	switch ext.KindOf(err) {
	case ext.KindInvalid, ext.KindMalformed, ext.KindDisabled, ext.KindNotFound, ext.KindConflict, ext.KindDenied:
		if err != nil {
			return fmt.Errorf("%w: %v", ErrPermanent, err)
		}
	case ext.KindInternal, ext.KindUnavailable:
	}
	return err
}

// newDeliveryID mints a delivery id: random, so two identical notifications a
// second apart are two deliveries.
func newDeliveryID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "no-delivery-id"
	}
	return "ntf_" + hex.EncodeToString(b[:])
}
