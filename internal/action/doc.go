// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package action is Control's side of the inbound action channel (0039, M15):
// the ext.ActionExecutor a registered ext.ActionHandler acts through, and the
// lifecycle that starts that handler.
//
// # What it is and is not
//
// An external system, a SOAR platform above all, decides a session is hostile
// and wants it ended, a person locked out, a grant revoked, a host fenced off.
// The handler owns the transport and its authentication; Control owns the
// effect. This package is the effect, and it is the complete list of what an
// inbound action can do: five kinds, each performed through the service an
// operator's north-bound route calls (internal/standing, internal/access,
// internal/revoke), never beside it. A handler can pull no lever an operator
// cannot (M15).
//
// # What an execution guarantees
//
//   - Idempotent per tenant on the action's id (M16's precedent for an inbound,
//     privileged, third-party path). The same id with the same action returns
//     the first result and changes nothing, but sends again what the first
//     execution owed the revocation stream, which is how a retry after
//     KindUnavailable gets its kill delivered; the same id with another action
//     is a conflict. Two calls under one id have one effect: they serialise on
//     an advisory lock, and the act, its audit record and its ledger row commit
//     together.
//   - Scoped to Action.Tenant (M12, M18). An id that exists only in another
//     tenant is not found.
//   - Audited, always: the act's own record when it did something, `action.unchanged`
//     when it found nothing to do, `action.refused` when it refused. Each names
//     the action id, the kind, the external reference, the reason code and the
//     provider, and the actor is `action-handler:<provider>`.
//   - Never silent. An error is classified (ext.Kind), a refusal made on purpose
//     is KindDenied and the one thing that can be an answer, and a nil error is
//     returned only for something that was done or found to be already so.
//
// # Start
//
// A handler that fails to Start does not stop Control: access must not depend
// on somebody else's automation (M11). The failure is logged, the point reports
// the handler as registered and not started, and nothing is executed.
package action
