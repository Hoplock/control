// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http"

	"github.com/hoplock/control/internal/httpapi/north"
)

// WriteError answers a host route's request in Control's error envelope (M21):
// a stable code, typed parameters, an English message, and the correlation id
// the request is logged under.
//
//	{"code": "report_not_found", "message": "...", "parameters": {...}, "correlation_id": "..."}
//
// The code is the thing a client depends on, so it must be one of Control's or
// one the host declared in Options.ErrorCodes. The message is English for curl
// and logs; the console builds its own sentence from the code and parameters.
//
// IT FAILS CLOSED. Each of these is a host programming error, answered as
// `500 internal` with the correlation id and logged as one:
//
//   - a call outside a host route;
//   - a code neither declared nor Control's;
//   - a status outside 4xx and 5xx;
//   - a 401, or the code "unauthenticated": on this surface "you are not
//     signed in" is Control's middleware's answer alone (M11);
//   - the code "internal" with a status that is not 5xx: it always means an
//     outage, never a refusal (M11);
//   - an empty message.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code string, params map[string]any, message string) {
	north.WriteHostError(w, r, status, code, params, message)
}
