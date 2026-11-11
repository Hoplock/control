// STANDING DENIALS, AS THE CONSOLE READS THEM (0055; PLAN M28, M11).
//
// A lock, a quarantine and the directory's deactivation are each a deny made
// on purpose, so each reads as a deny-class status (DESIGN.md §6) and never
// an outage. A lock and a deactivation are placed and lifted by different
// parties — an operator, and the directory — so they are two states, never
// one, and a subject can hold both: the record lists the lock first, and so
// does the console.

import type { ApiError } from "../i18n/errors";
import type { StatusName } from "../components/Status";
import type { StandingActor, Subject, Target } from "../api/types";

/**
 * What a lock with no reason shows its holder: the server's own words
 * (internal/standing.LockedByAdministrator), sent verbatim, never translated.
 */
export const DEFAULT_LOCK_TEXT = "Your access has been suspended by an administrator.";

/** The holder's text, as the server holds it to: one line, at most this many bytes (access.MaxRevokeReasonLen). */
export const MAX_HOLDER_TEXT_BYTES = 512;
/** A reason code: one line, at most this many bytes (access.MaxReasonCodeLineLen). */
export const MAX_REASON_CODE_BYTES = 128;

/** The prefixes a standing denial's actor carries when no person placed it. */
const ACTION_HANDLER = "action-handler:";
const IDENTITY_SYNC = "identity-sync:";

export function utf8Bytes(s: string): number {
  return new TextEncoder().encode(s).length;
}

const CONTROL = /[\u0000-\u001f\u007f-\u009f]/;

export type LineProblem = "control" | "too_long";

/**
 * Why a one-line text the server shows or records would be refused, checked
 * as it is typed: a line break or control character, or more than `limit`
 * bytes once trimmed. The server checks again and is the authority.
 */
export function lineProblem(text: string, limit: number): LineProblem | null {
  const v = text.trim();
  if (CONTROL.test(v)) return "control";
  if (utf8Bytes(v) > limit) return "too_long";
  return null;
}

/** What the holder of a lock will see: the text typed, trimmed, or the server's own when it is empty. */
export function holderText(typed: string): { text: string; isDefault: boolean } {
  const v = typed.trim();
  return v ? { text: v, isDefault: false } : { text: DEFAULT_LOCK_TEXT, isDefault: true };
}

/** A subject's standing denials, in the order the record holds them: the lock, then the deactivation. */
export function subjectDenials(s: Pick<Subject, "locked" | "deactivated">): StatusName[] {
  const out: StatusName[] = [];
  if (s.locked) out.push("locked");
  if (s.deactivated) out.push("deactivated");
  return out;
}

export function targetDenials(t: Pick<Target, "quarantined">): StatusName[] {
  return t.quarantined ? ["quarantined"] : [];
}

/** Who placed a denial: a person, an inbound action's handler, the directory's sync, or a credential with no person. */
export type Placer =
  | { kind: "person"; subject: string; principal: string; breakGlass: boolean }
  | { kind: "handler"; provider: string }
  | { kind: "sync"; provider: string }
  | { kind: "credential"; principal: string; breakGlass: boolean };

export function placerOf(a: StandingActor): Placer {
  if (a.principal_id.startsWith(ACTION_HANDLER)) return { kind: "handler", provider: a.principal_id.slice(ACTION_HANDLER.length) };
  if (a.principal_id.startsWith(IDENTITY_SYNC)) return { kind: "sync", provider: a.principal_id.slice(IDENTITY_SYNC.length) };
  if (a.subject) return { kind: "person", subject: a.subject, principal: a.principal_id, breakGlass: a.break_glass };
  return { kind: "credential", principal: a.principal_id, breakGlass: a.break_glass };
}

/**
 * A lock or quarantine that IS in force whose kill or invalidation could not
 * be published (503 standing_denial_undelivered): never a failure to place
 * it, and repaired only by asking again.
 */
export function isUndelivered(error: ApiError): boolean {
  return error.code === "standing_denial_undelivered";
}
