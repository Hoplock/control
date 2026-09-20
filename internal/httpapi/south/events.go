// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package south

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// `GET /v1/proxies/{proxy_id}/events` — the only route this server has to a
// running proxy.
//
// It is a different shape from every other endpoint here and each difference
// is forced by what it is:
//
//   - IT DOES NOT END. So it cannot run under the chain's request timeout,
//     which would turn a subscription into a ten-second connection and the
//     fleet into a reconnect storm. [Server.withLimits] exempts it by name.
//   - EVERY LINE IS FLUSHED. A kill switch behind a buffered writer is a
//     delayed kill switch, and the delay is however long it takes the next
//     event to fill a 4KiB buffer — which for an idle proxy is forever.
//   - IT ANSWERS 200 BEFORE IT HAS ANYTHING TO SAY. Everything that could
//     refuse the subscription is decided before the first byte; after it, the
//     only way to report a failure is to end the stream, so there is nothing
//     left that may fail differently.

// EventStream is the revocation broker this listener serves (`internal/revoke`).
//
// It is an interface here rather than the concrete broker for the reason every
// other seam on this listener is: the handler translates the wire shape and
// nothing else, and a test that needs a stalled writer or an empty replay
// buffer should not need a bus to get one.
type EventStream interface {
	// Subscribe serves one subscription and blocks until it ends. emit is
	// called for each event in order; its first error ends the stream.
	Subscribe(ctx context.Context, tenant store.Tenant, proxyID, lastEventID string, emit func(*contract.RevocationEvent) error) error
	// HeartbeatInterval is the interval the broker keeps and advertises. It
	// is read here to bound ONE WRITE: a proxy that has stopped reading
	// must not hold a goroutine and a connection open indefinitely, and the
	// heartbeat is what guarantees a write is attempted often enough for
	// the deadline to notice.
	HeartbeatInterval() time.Duration
}

// writeTimeoutFactor turns the heartbeat interval into the deadline on one
// write.
//
// It is a multiple rather than a constant because the interval is what makes
// the deadline meaningful: with heartbeats every 5s, a write that has not
// completed in 20s is a peer that is not reading, not a slow one. Two
// consecutive heartbeats plus the interval's own slack is the same shape as
// the proxy's own 20s reconnect timeout against a 10s ceiling.
const writeTimeoutFactor = 4

// subscribeEvents serves the stream.
func (h handlers) subscribeEvents(w http.ResponseWriter, r *http.Request) {
	caller, ok := callerFrom(r.Context())
	if !ok {
		h.s.writeError(w, r, errNoCaller)
		return
	}
	proxyID := r.PathValue("proxy_id")
	if proxyID == "" {
		h.s.writeError(w, r, invalid("proxy_id is required"))
		return
	}
	// A token bound to one proxy may subscribe only to that proxy's stream.
	// This is a DECISION about a credential and so it is a 401 (M11): a
	// token issued to an edge proxy reading another proxy's kill switch
	// would learn which of its neighbours' sessions are being ended.
	if !caller.Authorises(proxyID) {
		h.s.logDeny(r.Context(), contract.PathProxyEvents, "the proxy credential does not authorise this proxy id",
			"proxy_id", proxyID, "token_id", caller.TokenID)
		h.s.writeError(w, r, rejectCredential())
		return
	}

	// The subscription is opened BEFORE the status line, so a broker that
	// refuses one is still a classified failure with the contract's
	// envelope. Everything after the first byte can only end the stream.
	lastEventID := r.URL.Query().Get("last_event_id")

	ctrl := openStream(w)

	err := h.Subscribe(r.Context(), proxyID, lastEventID, h.s.streamWriter(w, ctrl))
	if err != nil && !errors.Is(err, context.Canceled) {
		// There is no status code left to say this with, so it is said
		// in the log. A truncated stream is what the proxy sees, and it
		// reconnects — which is the correct answer to every failure
		// that can arrive here.
		h.s.log.ErrorContext(r.Context(), "revocation stream ended in failure",
			"event", "southbound_stream_failed",
			"path", contract.PathProxyEvents,
			"proxy_id", proxyID,
			"tenant", caller.Tenant.String(),
			"correlation_id", correlationIDFrom(r.Context()),
			"error", err.Error(),
		)
	}
}

// openStream writes the status line and the stream's headers.
//
// It is the only function on this listener besides [Server.endpoint] that
// writes a success status, and `TestOnlyTheMapperNamesAStatusCode` names it
// for that reason: the discipline is that no handler chooses what a caller is
// TOLD — a refusal goes through the mapper — and the 200 here is the same
// success status the mapper's success path writes, moved forward because a
// stream has to be open before it has anything to say.
func openStream(w http.ResponseWriter) *http.ResponseController {
	w.Header().Set("Content-Type", contract.MediaTypeNDJSON)
	// A stream is not a document: an intermediary that buffers it to decide
	// a length has turned a kill switch into a delayed one.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	ctrl := http.NewResponseController(w)
	// Flushed on its own, so a proxy knows the subscription is open before
	// the first event rather than only when one happens.
	_ = ctrl.Flush()
	return ctrl
}

// Subscribe implements contract.EventPublisher.
//
// The tenant is read from the context rather than taken as an argument because
// it is NOT ON THE WIRE (M18): the credential resolved it, and the contract's
// handler seam takes only what the contract carries.
func (h handlers) Subscribe(ctx context.Context, proxyID, lastEventID string, emit func(*contract.RevocationEvent) error) error {
	caller, ok := callerFrom(ctx)
	if !ok {
		return errNoCaller
	}
	return h.s.events.Subscribe(ctx, caller.Tenant, proxyID, lastEventID, emit)
}

// streamWriter renders one event as one line and pushes it out.
func (s *Server) streamWriter(w http.ResponseWriter, ctrl *http.ResponseController) func(*contract.RevocationEvent) error {
	timeout := writeTimeoutFactor * s.heartbeatInterval()
	return func(ev *contract.RevocationEvent) error {
		line, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		// A deadline per write, so a peer that has stopped reading costs
		// one goroutine for one timeout rather than for the life of the
		// process. Not every ResponseWriter supports one — the error is
		// ignored rather than failing the stream over a capability.
		//
		// It is the WALL CLOCK rather than this server's injectable one,
		// and deliberately: a write deadline is a bound on a socket, and
		// a test that freezes the logical clock would otherwise set every
		// deadline in the past and cut the stream it is grading.
		_ = ctrl.SetWriteDeadline(time.Now().Add(timeout))
		if _, err := w.Write(append(line, '\n')); err != nil {
			return err
		}
		return ctrl.Flush()
	}
}

func (s *Server) heartbeatInterval() time.Duration {
	if s.events == nil {
		return DefaultHeartbeatInterval
	}
	d := s.events.HeartbeatInterval()
	if d <= 0 {
		return DefaultHeartbeatInterval
	}
	return d
}

// DefaultHeartbeatInterval is the interval this listener assumes when the
// broker states none. It bounds a write deadline and nothing else — the
// interval that is kept and advertised belongs to the broker.
const DefaultHeartbeatInterval = 5 * time.Second

// isEventStream reports whether a path is the revocation stream.
//
// It matches the shape rather than the route, because the middleware chain
// runs BEFORE the mux has resolved one: `/v1/proxies/{proxy_id}/events` with a
// single non-empty segment where the id goes. A path that only looks like it
// gets the exemption and then a 404, which costs nothing — the exemption is
// from a timeout, not from authentication.
func isEventStream(path string) bool {
	const prefix = "/v1/proxies/"
	const suffix = "/events"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return false
	}
	id := path[len(prefix) : len(path)-len(suffix)]
	return id != "" && !strings.Contains(id, "/")
}
