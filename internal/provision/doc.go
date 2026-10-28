// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package provision is Control's side of identity provisioning (0040, M15):
// the ext.SyncSink a registered ext.IdentitySync applies what it learned
// through, and the lifecycle that starts that sync.
//
// # What it is and is not
//
// A customer's directory (SCIM, most often) knows who works here and who has
// left. The sync owns the protocol and its listener; Control owns the effect.
// This package is the effect: it provisions subjects ahead of their first
// login, keeps their groups and attributes in step, and de-provisions them, so
// a person disabled in the directory loses their access when the directory
// says so rather than at their next failed login.
//
// # What a call guarantees
//
//   - Joined on ExternalID, never on the username. Control allocates the
//     subject id the first time it links an id, and the link row is the join.
//   - Idempotent on ExternalID, and ordered: an upsert is applied only when
//     its UpdatedAt is later than the last one applied, so a replay or a stale
//     write changes nothing and writes no record.
//   - The directory is the only writer of what it provisions. Its groups and
//     attributes reach policy only through the tenant's claim mapping (M7),
//     exactly as a login's claims do.
//   - A deactivation is a standing denial of its own (M28): every authorize
//     for the subject is a deny with `subject_deactivated` from the instant it
//     commits, and only the directory lifts it. Then everything the subject
//     holds is revoked through the services an operator's levers call.
//   - Scoped to the call's tenant (M12, M18), audited on that tenant's control
//     stream with the sync as the actor (`identity-sync:<provider>`), and never
//     silent: an error is classified, and nil is returned only for something
//     done or found to be already so.
//
// # Start
//
// A sync that fails to Start does not stop Control: access must not depend on
// somebody else's software (M11). The failure is logged, the point reports the
// sync as registered and not started, and Control serves.
package provision
