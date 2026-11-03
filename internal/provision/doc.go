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
//   - Joined on the directory's source and ExternalID (0042), never on the
//     username. Control allocates the subject id the first time it links a
//     source's id, and the link row is the join. Two directories feeding one
//     tenant keep their own ids, and a subject's groups resolve within its own
//     source; a login name is still unique across sources.
//   - Idempotent on (tenant, source, ExternalID), and ordered per that key: an
//     upsert is applied only when its UpdatedAt is later than the last one
//     applied, so a replay or a stale write changes nothing and writes no
//     record.
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
// # A new claim mapping
//
// A directory subject's row names the claim mapping version it was derived
// under, and a Recalculator brings every one that names another version onto
// the tenant's active mapping by itself (0043): started by a mapping written
// through the API, and by a sweep of the tenants this process has seen. It is
// built from the store and the revocation stream alone, so it runs with no sync
// registered, and it recalculates through the same derive, lock order and
// delivery as a sink write.
//
// # Start
//
// A sync that fails to Start does not stop Control: access must not depend on
// somebody else's software (M11). The failure is logged, the point reports the
// sync as registered and not started, and Control serves.
package provision
