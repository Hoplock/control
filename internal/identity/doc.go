// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package identity resolves who is asking, and owns the MFA conversation
// end to end (PLAN §6, M7). The proxy never talks to an IdP and never contacts
// an MFA provider: everything identity-shaped that it relays is resolved here.
//
// Two seams are what 0011 replaces — [Directory], where identities and their
// credentials come from, and [MFAProvider], which delivers a second factor out
// of band. Federated brokers (OIDC, SAML) and the explicit, versioned mapping
// from IdP claims onto policy attributes slot in behind the first.
//
// What must NOT move behind either seam is anything in [Service] that is about
// the CONVERSATION rather than about the factor: challenge lifetime, poll-rate
// enforcement, single use, and expiry as a deny. Those are the same whoever
// supplies the factor, and a provider that had to re-implement them would get
// one of them wrong.
//
// Everything here answers [Outcome] beside an error, and the split is M11 made
// structural: a non-nil error is an OUTAGE with nothing to inspect, and a deny
// is a field only code that built one on purpose can set.
package identity
