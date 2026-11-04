// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"net/http"
	"time"
)

// ServedByHeader names who answered a request on a [ServeTable] handler.
const ServedByHeader = "X-Test-Served-By"

// ServeTable serves the table [Table] lists, with no services behind it: each
// of Control's routes answers 204 with [ServedByHeader] set to "control" and
// its route, and a host's route runs the host's handler. A path nobody serves
// is answered by the same not-found handler, and a method a pattern does not
// serve by the same dispatch, as on a real server.
//
// It is what lets a test ask, at the request level and for every route of
// Control's real table, which handler the mux picks, without a database.
func ServeTable(provider string, routes []HostRoute) (http.Handler, error) {
	s, err := tableServer(provider, routes, nil, func(r Route) http.Handler {
		if r.Provider != "" {
			return r.Handler
		}
		served := "control " + r.Method + " " + r.Pattern
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(ServedByHeader, served)
			w.WriteHeader(http.StatusNoContent)
		})
	})
	if err != nil {
		return nil, err
	}
	return s.withCorrelationID(s.router), nil
}

// BoundFor is the deadline withLimits gives r: its route's own Timeout, or the
// listener's request timeout (0044).
func (s *Server) BoundFor(r *http.Request) time.Duration {
	if d, ok := s.router.boundFor(r); ok {
		return d
	}
	return s.requestTimeout
}

// ReportTimeout is the bound the generation route was registered with.
func (s *Server) ReportTimeout() time.Duration { return s.reportTimeout }
