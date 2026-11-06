// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"unicode"
	"unicode/utf8"

	"github.com/hoplock/control/internal/access"
)

// The supervising operator a supervisor's call names (0048, PLAN M19).
//
// A supervisor holds one credential per supervised deployment, and its plane
// acts through it for many people. So a supervisor's call may say which of
// them it acts for, and the deployment writes that name on every audit record
// the call writes, beside the `sup-` principal it verified.
//
// THE NAME IS THE SUPERVISOR'S WORD, NOT THIS SERVER'S VERIFICATION. What was
// verified is the credential, and nothing about it changes: the actor of every
// act is still built from the credential (audit/operatorevent.go's rule). The
// name is carried beside the actor and never in its place. It is never a
// principal or a subject, never matched, and never read by RBAC, tenant
// resolution, the decision path, or any filter: a route's answer does not
// depend on it. It is written down and shown, and that is all.
//
// It is checked once, here, after authentication and before a handler runs,
// so a bad credential is a 401 whatever the header says, and a refused header
// changes nothing.

const (
	// ActingOperatorHeader carries the name of the supervising operator a
	// supervisor's call acts for.
	ActingOperatorHeader = "Hoplock-Acting-Operator"
	// MaxActingOperatorRunes is the longest name accepted, in characters
	// once decoded.
	MaxActingOperatorRunes = 128
	// ProblemNotSupervisor is the header sent by a caller that is not a
	// supervisor. Only a `sup-` principal may name who it acts for, so an
	// ordinary credential cannot put a name in a record.
	ProblemNotSupervisor = "not_supervisor"
)

// DecodeActingOperator decodes and checks one value of
// [ActingOperatorHeader], returning the name, or the problem that refuses it.
//
// A header is bytes, and few names are ASCII, so the value is the name's
// UTF-8 with every octet outside printable ASCII, and `%` itself,
// percent-encoded (RFC 3986 §2.1). A raw octet outside printable ASCII is
// therefore not a value this header can carry. Decoded, the name must be
// valid UTF-8, 1 to [MaxActingOperatorRunes] characters, every one of them
// [unicode.IsPrint] — letters, marks, numbers, punctuation, symbols and the
// ASCII space, and no control or format character, so a bidirectional
// override cannot make a record display a name it does not carry — with no
// leading or trailing space. Over the limit is [access.ProblemTooLong];
// anything else wrong is [access.ProblemInvalid].
//
// The name is returned exactly as decoded: no case folding, no normalisation,
// no trimming.
func DecodeActingOperator(raw string) (name, problem string) {
	for i := 0; i < len(raw); i++ {
		if raw[i] < 0x20 || raw[i] > 0x7e {
			return "", access.ProblemInvalid
		}
	}
	name, err := url.PathUnescape(raw)
	if err != nil || name == "" || !utf8.ValidString(name) {
		return "", access.ProblemInvalid
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return "", access.ProblemInvalid
		}
	}
	if first, _ := utf8.DecodeRuneInString(name); first == ' ' {
		return "", access.ProblemInvalid
	}
	if last, _ := utf8.DecodeLastRuneInString(name); last == ' ' {
		return "", access.ProblemInvalid
	}
	if utf8.RuneCountInString(name) > MaxActingOperatorRunes {
		return "", access.ProblemTooLong
	}
	return name, ""
}

// ActingOperatorFrom returns the supervising operator this request's
// supervisor named, decoded and checked. It is false on every request that
// named none, and on every request whose caller is not a supervisor, since
// such a request is refused before a handler runs.
//
// It is for the record and the log line, never for a decision: see the head
// of this file.
func ActingOperatorFrom(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(ctxKeyActingOperator).(string)
	return name, ok && name != ""
}

// actingOperator reads the header off r: whether it was sent, and if it was,
// the name or the problem that refuses it. A header sent more than once is
// refused, not resolved: which of two names is the operator is not this
// server's to choose.
func actingOperator(r *http.Request) (name, problem string, present bool) {
	values := r.Header.Values(ActingOperatorHeader)
	switch len(values) {
	case 0:
		return "", "", false
	case 1:
		name, problem = DecodeActingOperator(values[0])
		return name, problem, true
	default:
		return "", access.ProblemInvalid, true
	}
}

// refuseActingOperator answers a request whose header this server will not
// record. Nothing has run: the refusal is written before the handler.
func refuseActingOperator(w http.ResponseWriter, r *http.Request, problem string) {
	limit := ""
	if problem == access.ProblemTooLong {
		limit = strconv.Itoa(MaxActingOperatorRunes)
	}
	invalid(w, r, ActingOperatorHeader, problem, limit)
}
