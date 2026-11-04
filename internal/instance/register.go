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
// which both directions are newline-delimited JSON:
//
//	deployment → supervisor (the request body):
//	  {"kind":"hello","id":<head>,"protocol":1,"data":<identity>}
//	  {"kind":"resync","id":<head>,"data":<report>}          when replay is impossible
//	  {"kind":<event kind>,"id":"evt-<epoch>-<seq>","at":…,"origin":…,"data":…}
//	  {"kind":"report","id":"evt-<epoch>-<seq>.<line>","data":<report>}  on request
//	  {"kind":"heartbeat","id":"evt-<epoch>-<seq>.<line>"}
//
//	supervisor → deployment (the response body):
//	  {"kind":"resume","last_event_id":"evt-…"}               first, always ("" when new)
//	  {"kind":"heartbeat"}                                    liveness
//	  {"kind":"rereport"}                                     send a report now
//
// The guarantees are 0009's: heartbeats on one interval, monotonic event ids,
// replay from the supervisor's last event id, and `resync` — a full report —
// when the id is under another epoch or below the feed's floor. Since the feed
// is in the store (feed.go), a registration that moves to another node resumes
// rather than resyncing.
//
// INBOUND, NOTHING ACTS. A line that is not a heartbeat or a re-report request
// is logged and ignored. Administrative action from a supervisor is a
// north-bound API call with a scoped credential, subject to RBAC and audit
// like any other — that separation keeps this channel from becoming a
// privileged inbound surface.

// ProtocolVersion is the registration protocol's version, on the hello line.
const ProtocolVersion = 1

// SupervisionJob is the cluster singleton the registration holds.
const SupervisionJob = "hoplock.supervision"

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

// revoked reads the local revocation. An unreadable store reads as not
// revoked for this check only: the session keeps the connection it has, and
// the next check reads again.
func (r *Registration) revoked(ctx context.Context) bool {
	d, err := r.svc.o.Store.Deployment().Identity(ctx)
	return err == nil && !d.SupervisionRevokedAt.IsZero()
}

// Run holds the registration until ctx ends. While the operator has revoked it
// locally it does not connect, and it ends any connection it holds within one
// heartbeat; when they restore it, it connects again — neither needs a
// restart, and neither needs the supervisor.
func (r *Registration) Run(ctx context.Context) {
	backoff := r.o.MinBackoff
	for ctx.Err() == nil {
		if r.revoked(ctx) {
			sleep(ctx, r.o.Heartbeat)
			continue
		}
		err := r.session(ctx)
		if ctx.Err() != nil {
			return
		}
		switch {
		case errors.Is(err, errRevoked):
			r.o.Logger.Info("the supervisory registration was revoked locally; disconnected",
				"event", "supervision_revoked")
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
			if r.revoked(ctx) {
				return errRevoked
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
