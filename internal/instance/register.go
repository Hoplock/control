// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package instance

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoplock/control/ext"
)

// The supervisory registration (M19): one long-lived outbound connection to a
// supervisor, with 0009's stream shape pointed the other way.
//
// A supervised deployment sits behind NAT and the supervisor cannot dial in —
// the situation proxy D11 solved one level down by having a downstream proxy
// register outbound. So this deployment dials, and holds ONE connection on
// which both directions are newline-delimited JSON. The number after each line
// is the registration protocol version it arrived in:
//
//	deployment → supervisor (the request body):
//	  {"kind":"hello","id":<head>,"protocol":2,"data":<identity>}               1
//	  {"kind":"resync","id":<head>,"data":<report>}                              1  when replay is impossible
//	  {"kind":<event kind>,"id":"evt-<epoch>-<seq>","at":…,"origin":…,"data":…}  1
//	  {"kind":"report","id":"evt-<epoch>-<seq>.<line>","data":<report>}         1  on request
//	  {"kind":"heartbeat","id":"evt-<epoch>-<seq>.<line>"}                       1
//	  {"kind":"revoked","id":"evt-<epoch>-<seq>.<line>","at":<revoked at>}       2  last, on a local revocation only
//
//	supervisor → deployment (the response body):
//	  {"kind":"resume","last_event_id":"evt-…"}   1  first, always ("" when new)
//	  {"kind":"heartbeat"}                         1  liveness
//	  {"kind":"rereport"}                          1  send a report now
//
// The guarantees are 0009's: heartbeats on one interval, monotonic event ids,
// replay from the supervisor's last event id, and `resync` — a full report —
// when the id is under another epoch or below the feed's floor. Since the feed
// is in the store (feed.go), a registration that moves to another node resumes
// rather than resyncing.
//
// THE VERSION, AND THE SKIP RULE. hello's `protocol` is the version this
// deployment speaks. It is informational: nothing negotiates it, every line of
// that version can arrive, and a supervisor reads it to know which. A new line
// kind or a new member on a line, in either direction, moves it by one. A
// supervisor skips a line kind, or a member, it does not recognise, and never
// refuses one, as this side ignores an inbound kind it does not act on. A
// changed meaning of an existing line or member is not covered, because
// skipping cannot make it safe. The rule is docs/API-VERSIONING.md's ("The
// registration protocol"), and the north-bound API's version does not move
// with this one.
//
// REVOKED. When the check each heartbeat runs sees that the deployment's
// operator revoked the registration locally (`supervision revoke`), the
// session writes `revoked` — the heartbeat's id form, `at` the instant the
// store holds, no `by` — ends the request body cleanly, and waits for the
// supervisor to end its response, all within one heartbeat of that check
// (endRevoked). Nothing else ends with it: a shutdown, a dropped connection, a
// failed write, a lost singleton and a silent supervisor send no line, so its
// absence means nothing.
//
// INBOUND, NOTHING ACTS. A line that is not a heartbeat or a re-report request
// is logged and ignored. Administrative action from a supervisor is a
// north-bound API call with a scoped credential, subject to RBAC and audit
// like any other — that separation keeps this channel from becoming a
// privileged inbound surface.

// ProtocolVersion is the registration protocol's version, on the hello line.
// docs/API-VERSIONING.md says what moves it.
const ProtocolVersion = 2

// SupervisionJob is the cluster singleton the registration holds.
const SupervisionJob = "hoplock.supervision"

// OutboundRevoked is the last line of a registration whose deployment's
// operator revoked it locally (protocol 2). It names a position without
// taking one, as a heartbeat does: it is not an event of the feed, is never
// logged there, and is never replayed.
const OutboundRevoked = "revoked"

// Inbound line kinds.
const (
	InboundResume    = "resume"
	InboundHeartbeat = "heartbeat"
	InboundRereport  = "rereport"
)

// Line is one line of the registration, in either direction.
type Line struct {
	Kind        string          `json:"kind"`
	ID          string          `json:"id,omitempty"`
	At          *time.Time      `json:"at,omitempty"`
	Origin      string          `json:"origin,omitempty"`
	Protocol    int             `json:"protocol,omitempty"`
	LastEventID string          `json:"last_event_id,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
}

// RegistrationOptions configure a Registration.
type RegistrationOptions struct {
	// Endpoint is the supervisor's registration URL. Required.
	Endpoint string
	// Credential is the bearer credential this deployment presents. It is
	// sent and never logged. Required.
	Credential string
	// Client dials the supervisor. Nil takes one with Timeouts' bounds.
	Client *http.Client
	// Heartbeat is the interval this side sends a heartbeat on and reads
	// the feed on. Liveness is how long without a line from the supervisor
	// before the connection is dropped and redialled.
	Heartbeat time.Duration
	Liveness  time.Duration
	// DialTimeout bounds connecting and waiting for the supervisor's
	// response headers.
	DialTimeout time.Duration
	// MinBackoff and MaxBackoff bound the wait between attempts.
	MinBackoff, MaxBackoff time.Duration
	Logger                 *slog.Logger
}

// Registration holds the outbound registration.
type Registration struct {
	o    RegistrationOptions
	svc  *Service
	feed *Feed
	line atomic.Uint64

	// Sessions counts connections that reached the supervisor; tests read
	// it, and it is in the log.
	sessions atomic.Int64
}

// Errors the registration ends a session with.
var (
	errRevoked        = errors.New("instance: the supervisory registration was revoked locally")
	errNoResume       = errors.New("instance: the supervisor did not say where to resume")
	errSupervisorGone = errors.New("instance: the supervisor stopped answering")
)

// NewRegistration builds a registration over svc and its feed.
func NewRegistration(svc *Service, feed *Feed, o RegistrationOptions) (*Registration, error) {
	if o.Endpoint == "" || o.Credential == "" {
		return nil, errors.New("instance: a supervisor endpoint and a credential are both required")
	}
	if o.Heartbeat <= 0 {
		o.Heartbeat = 5 * time.Second
	}
	if o.Liveness <= 0 {
		o.Liveness = 3 * o.Heartbeat
	}
	if o.DialTimeout <= 0 {
		o.DialTimeout = 10 * time.Second
	}
	if o.MinBackoff <= 0 {
		o.MinBackoff = time.Second
	}
	if o.MaxBackoff < o.MinBackoff {
		o.MaxBackoff = max(o.MinBackoff, time.Minute)
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Client == nil {
		o.Client = &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ResponseHeaderTimeout: o.DialTimeout,
			TLSHandshakeTimeout:   o.DialTimeout,
			ForceAttemptHTTP2:     true,
		}}
	}
	return &Registration{o: o, svc: svc, feed: feed}, nil
}

// Sessions is how many connections reached the supervisor.
func (r *Registration) Sessions() int64 { return r.sessions.Load() }

// revoked reads the local revocation, and the instant the store holds for it.
// The `revoked` line carries that instant and no other: never the time of
// the check, and never a second read, which a restore in between would leave
// with no time to send. An unreadable store reads as not revoked for this
// check only: the session keeps the connection it has, and the next check
// reads again.
func (r *Registration) revoked(ctx context.Context) (time.Time, bool) {
	d, err := r.svc.o.Store.Deployment().Identity(ctx)
	if err != nil || d.SupervisionRevokedAt.IsZero() {
		return time.Time{}, false
	}
	return d.SupervisionRevokedAt, true
}

// revokedEnding is how a session ended when its check saw a local
// revocation: the instant its `revoked` line carried, and whether the
// supervisor was told.
type revokedEnding struct {
	at time.Time
	// told is true when the line and the clean end of the request body were
	// written and the supervisor then ended its response, within the bound.
	// That is the only evidence this side has that the supervisor read them:
	// a line written into a connection the supervisor has stopped reading is
	// still written, and it was never told.
	told bool
}

func (e *revokedEnding) Error() string { return errRevoked.Error() }
func (e *revokedEnding) Unwrap() error { return errRevoked }

// Run holds the registration until ctx ends. While the operator has revoked it
// locally it does not connect, and a connection it holds sees the revocation
// within one heartbeat and is ended within one more, its last line saying so;
// when they restore it, it connects again — neither needs a restart, and
// neither needs the supervisor.
func (r *Registration) Run(ctx context.Context) {
	backoff := r.o.MinBackoff
	for ctx.Err() == nil {
		if _, revoked := r.revoked(ctx); revoked {
			sleep(ctx, r.o.Heartbeat)
			continue
		}
		err := r.session(ctx)
		if ctx.Err() != nil {
			return
		}
		var ended *revokedEnding
		switch {
		case errors.As(err, &ended):
			r.o.Logger.Info("the supervisory registration was revoked locally; disconnected",
				"event", "supervision_revoked", "revoked_at", ended.at.Format(time.RFC3339Nano),
				"told_supervisor", ended.told)
			backoff = r.o.MinBackoff
			continue
		case err != nil:
			r.o.Logger.Warn("the supervisory registration dropped; redialling",
				"event", "supervision_disconnected", "error", err.Error(), "retry_in", backoff.String())
		}
		sleep(ctx, backoff)
		backoff = min(backoff*2, r.o.MaxBackoff)
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// session is one connection, from dial to drop.
func (r *Registration) session(parent context.Context) error {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)

	dep := r.svc.o.Store.Deployment()
	stream, err := dep.SupervisionStream(ctx)
	if err != nil {
		return err
	}
	id, err := r.svc.Identity(ctx, nil)
	if err != nil {
		return err
	}

	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	// The transport reads the request body until it ends, and waits for that
	// reader before it gives up a request: closing the pipe with the session
	// is what lets a cancelled dial return at all.
	stopPipe := context.AfterFunc(ctx, func() { _ = pw.CloseWithError(context.Cause(ctx)) })
	defer stopPipe()
	w := &lineWriter{w: pw}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.o.Endpoint, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.o.Credential)
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("Accept", "application/x-ndjson")
	req.Header.Set("Hoplock-Instance-Id", id.InstanceID)

	identity, _ := json.Marshal(id)
	go func() {
		// Written while the transport sends the request: the hello is the
		// first thing the supervisor reads.
		if err := w.write(Line{Kind: "hello", ID: EventID(stream.Epoch, stream.Head), Protocol: ProtocolVersion, Data: identity}); err != nil {
			cancel(err)
		}
	}()

	// Bound the wait for the response headers by the dial timeout, whatever
	// the client does: a supervisor that accepts and never answers is case
	// (d) of the isolation test, and it must cost a redial, not a goroutine.
	dialTimer := time.AfterFunc(r.o.DialTimeout, func() { cancel(errSupervisorGone) })
	resp, err := r.o.Client.Do(req)
	dialTimer.Stop()
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("instance: the supervisor answered %d", resp.StatusCode)
	}
	r.sessions.Add(1)

	// The watchdog: no line from the supervisor within Liveness drops the
	// connection, including while a write is blocked on a stalled peer.
	watchdog := time.AfterFunc(r.o.Liveness, func() { cancel(errSupervisorGone) })
	defer watchdog.Stop()

	inbound := make(chan Line, 16)
	// supervisorEnded is set before inbound closes, and read only after: true
	// when the supervisor ended its response cleanly, rather than the
	// connection failing or this session cancelling it.
	var supervisorEnded bool
	go func() {
		defer close(inbound)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 4096), 64*1024)
		for sc.Scan() {
			var l Line
			if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
				r.o.Logger.Warn("a supervisor line did not decode; ignored", "event", "supervision_line_malformed")
				continue
			}
			watchdog.Reset(r.o.Liveness)
			select {
			case inbound <- l:
			case <-ctx.Done():
				return
			}
		}
		supervisorEnded = sc.Err() == nil
	}()

	// The first line says where to resume.
	var resume Line
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case l, ok := <-inbound:
		if !ok || l.Kind != InboundResume {
			return errNoResume
		}
		resume = l
	}

	cursor, err := r.resume(ctx, w, resume.LastEventID)
	if err != nil {
		return err
	}
	r.o.Logger.Info("registered with the supervisor", "event", "supervision_registered",
		"instance_id", id.InstanceID, "resumed_after", cursor)

	tick := time.NewTicker(r.o.Heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case l, ok := <-inbound:
			if !ok {
				return errSupervisorGone
			}
			switch l.Kind {
			case InboundHeartbeat:
			case InboundRereport:
				if err := r.sendReport(ctx, w, "report", cursor); err != nil {
					return err
				}
			default:
				// Nothing inbound acts (M19).
				r.o.Logger.Warn("a supervisor line of a kind this deployment does not act on was ignored",
					"event", "supervision_line_ignored", "kind", l.Kind)
			}
		case <-tick.C:
			if at, revoked := r.revoked(ctx); revoked {
				// The epoch is the deployment's, set once when its row is
				// created, so the one this session read is the feed's
				// current one: the ending needs no store read that could fail.
				id := lineID(stream.Epoch, cursor, r.line.Add(1))
				return r.endRevoked(ctx, cancel, w, pw, inbound, func() bool { return supervisorEnded }, id, at)
			}
			if cursor, err = r.send(ctx, w, cursor); err != nil {
				return err
			}
			st, err := dep.SupervisionStream(ctx)
			if err != nil {
				return err
			}
			if err := w.write(Line{Kind: "heartbeat", ID: lineID(st.Epoch, cursor, r.line.Add(1))}); err != nil {
				return err
			}
		}
	}
}

// endRevoked ends a session whose check saw a local revocation (M19's first
// rule), in an order that lets the supervisor read why:
//
//  1. the `revoked` line;
//  2. a clean end of the request body (pw.Close, never CloseWithError), so the
//     supervisor reads the line and then the end of the body;
//  3. only then the response: this waits for the supervisor to end it, which
//     the reference supervisor does when it reads the end of the body.
//
// The session's deferred teardown closes the response before the pipe, and
// closing the response can tear the connection down before the transport has
// written what is in the pipe. That is harmless when nothing more is to be
// said, and it is why this ending closes the pipe itself and waits before it
// returns to that teardown.
//
// The three together are bounded by one heartbeat from the check. A
// supervisor that does not take them within it loses the connection at that
// bound — the session is cancelled, which closes the pipe and aborts the
// request — exactly as it lost it at the check before protocol 2, with no
// line. Nothing here can keep a revoked registration open past the bound,
// make it wait on the supervisor, or make any node dial again.
func (r *Registration) endRevoked(ctx context.Context, cancel context.CancelCauseFunc, w *lineWriter, pw *io.PipeWriter,
	inbound <-chan Line, supervisorEnded func() bool, id string, at time.Time) error {
	bound := time.AfterFunc(r.o.Heartbeat, func() { cancel(errRevoked) })
	defer bound.Stop()

	ended := &revokedEnding{at: at}
	if err := w.write(Line{Kind: OutboundRevoked, ID: id, At: &at}); err != nil {
		return ended
	}
	_ = pw.Close()
	for {
		select {
		case <-ctx.Done():
			return ended
		case _, ok := <-inbound:
			// A line the supervisor sends meanwhile is drained and not
			// acted on; its end of the response is what this waits for.
			if !ok {
				ended.told = ctx.Err() == nil && supervisorEnded()
				return ended
			}
		}
	}
}

// resume decides between replay and resync, sends what it decided, and
// returns the position the session continues after.
func (r *Registration) resume(ctx context.Context, w *lineWriter, last string) (int64, error) {
	st, err := r.svc.o.Store.Deployment().SupervisionStream(ctx)
	if err != nil {
		return 0, err
	}
	epoch, seq, ok := ParseEventID(last)
	if ok && epoch == st.Epoch && seq >= st.Floor && seq <= st.Head {
		return r.send(ctx, w, seq)
	}
	// A new supervisor, an id from another feed, or one below the floor:
	// what it missed is gone, so it gets the whole picture instead.
	if err := r.sendReport(ctx, w, "resync", st.Head); err != nil {
		return 0, err
	}
	return st.Head, nil
}

// send writes every logged event after cursor, and returns the new cursor.
func (r *Registration) send(ctx context.Context, w *lineWriter, cursor int64) (int64, error) {
	st, err := r.svc.o.Store.Deployment().SupervisionStream(ctx)
	if err != nil {
		return cursor, err
	}
	if cursor < st.Floor {
		// Pruned while this session was behind: resync, never skip.
		if err := r.sendReport(ctx, w, "resync", st.Head); err != nil {
			return cursor, err
		}
		return st.Head, nil
	}
	for {
		batch, err := r.svc.o.Store.Deployment().SupervisionAfter(ctx, cursor, 256)
		if err != nil {
			return cursor, err
		}
		for _, e := range batch {
			at := e.LoggedAt
			if err := w.write(Line{Kind: e.Kind, ID: EventID(st.Epoch, e.Seq), At: &at, Origin: e.Origin, Data: e.Body}); err != nil {
				return cursor, err
			}
			cursor = e.Seq
		}
		if len(batch) < 256 {
			return cursor, nil
		}
	}
}

func (r *Registration) sendReport(ctx context.Context, w *lineWriter, kind string, at int64) error {
	rep, err := r.feed.Snapshot(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	st, err := r.svc.o.Store.Deployment().SupervisionStream(ctx)
	if err != nil {
		return err
	}
	id := EventID(st.Epoch, at)
	if kind == "report" {
		id = lineID(st.Epoch, at, r.line.Add(1))
	}
	return w.write(Line{Kind: kind, ID: id, Data: body})
}

// lineWriter writes whole lines, one at a time.
type lineWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lineWriter) write(line Line) error {
	b, err := json.Marshal(line)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = l.w.Write(append(b, '\n'))
	return err
}

// --- the singleton --------------------------------------------------------

// Supervision runs the feed and the registration on whichever node holds the
// supervisory singleton (ext.ClusterCoordinator, 0004), and nowhere else. On
// one node the default coordinator grants it at once; in a cluster a real
// election decides, through the same code (M19). Losing the singleton drops
// the registration; gaining it registers again, resuming from the
// supervisor's last event id.
type Supervision struct {
	coord ext.ClusterCoordinator
	svc   *Service
	reg   *Registration
	feed  *Feed
	every time.Duration
	log   *slog.Logger
}

// NewSupervision builds the singleton runner.
func NewSupervision(coord ext.ClusterCoordinator, svc *Service, feed *Feed, reg *Registration, observeEvery time.Duration) *Supervision {
	if observeEvery <= 0 {
		observeEvery = svc.o.ReportInterval
	}
	return &Supervision{coord: coord, svc: svc, reg: reg, feed: feed, every: observeEvery, log: svc.o.Logger}
}

// Run campaigns for the singleton and holds the registration while it is held,
// until ctx ends.
func (s *Supervision) Run(ctx context.Context) error {
	lead, err := s.coord.Lead(ctx, SupervisionJob)
	if err != nil {
		return err
	}
	defer func() { _ = lead.Release(context.WithoutCancel(ctx)) }()

	var (
		stop context.CancelFunc
		done chan struct{}
	)
	halt := func() {
		if stop == nil {
			return
		}
		stop()
		<-done
		stop, done = nil, nil
		s.svc.SetJob(SupervisionJob, false)
		s.feed.Reset()
	}
	defer halt()
	for {
		select {
		case <-ctx.Done():
			return nil
		case held, ok := <-lead.Changes():
			if !ok {
				return nil
			}
			if !held {
				s.log.Info("the supervisory singleton moved away from this node", "event", "supervision_singleton_lost")
				halt()
				continue
			}
			if stop != nil {
				continue
			}
			s.log.Info("this node holds the supervisory singleton", "event", "supervision_singleton_gained")
			s.svc.SetJob(SupervisionJob, true)
			hctx, cancel := context.WithCancel(ctx)
			stop, done = cancel, make(chan struct{})
			go s.hold(hctx, done)
		}
	}
}

// hold runs the feed's observations and the registration until ctx ends.
func (s *Supervision) hold(ctx context.Context, done chan struct{}) {
	defer close(done)
	var wg sync.WaitGroup
	wg.Go(func() {
		tick := time.NewTicker(s.every)
		defer tick.Stop()
		for {
			if _, err := s.feed.Observe(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("the supervisory feed could not observe the deployment; it tries again",
					"event", "supervision_observe_failed", "error", err.Error())
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	})
	if s.reg != nil {
		wg.Go(func() { s.reg.Run(ctx) })
	}
	wg.Wait()
}
