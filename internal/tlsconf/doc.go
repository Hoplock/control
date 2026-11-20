// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package tlsconf builds the *tls.Config one listener serves with, from that
// listener's own `tls:` block (0063, M29).
//
// IT IS A CONSTRUCTOR, NOT SHARED MIDDLEWARE. M2 forbids the south-bound and
// north-bound listeners sharing a port, a middleware chain or a credential
// type; it does not forbid them calling the same pure function. Each listener
// calls Build with its own section and gets its own *tls.Config — its own
// certificate, its own client CAs, its own key-exchange requirement — and
// nothing built here is held, cached or handed to the other listener.
//
// The wire posture is this server's to state and assert, not the ingress's to
// be trusted with (M29). That is why the defaults live here, each with its
// reason, why Posture is what the start-up log prints, and why a `tls:` block
// that cannot be honoured refuses to start rather than serving plaintext.
package tlsconf
