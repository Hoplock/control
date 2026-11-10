// What the inventory screens decide without rendering (0054): how a label set
// or a list is written in a form and read back, how a grant's state reads in
// the status vocabulary, which field an API refusal is about, and what each
// fixed role is called. Unit-tested in test/inventory.test.ts.

import type { MessageKey } from "../i18n/catalogue";
import type { ApiError } from "../i18n/errors";
import type { GrantState, RoleBinding } from "../api/types";
import type { StatusName } from "../components/Status";

/** A label set as a form holds it: one `key=value` per line, keys in order. */
export function labelsText(labels: Readonly<Record<string, string>> | null | undefined): string {
  return Object.keys(labels ?? {})
    .sort()
    .map((k) => `${k}=${(labels as Record<string, string>)[k]}`)
    .join("\n");
}

/**
 * A label set read from a form: one `key=value` per line (or comma-separated),
 * blank lines skipped. A line with no `=` or an empty key is a mistake the
 * form refuses at the field, so it answers null rather than guessing.
 */
export function parseLabels(text: string): Record<string, string> | null {
  const out: Record<string, string> = {};
  for (const raw of text.split(/[\n,]/)) {
    const line = raw.trim();
    if (!line) continue;
    const at = line.indexOf("=");
    if (at <= 0) return null;
    const key = line.slice(0, at).trim();
    if (!key) return null;
    out[key] = line.slice(at + 1).trim();
  }
  return out;
}

/** A list read from a form: one item per line or comma, trimmed, blanks and repeats dropped. */
export function parseList(text: string): string[] {
  const out: string[] = [];
  for (const raw of text.split(/[\n,]/)) {
    const v = raw.trim();
    if (v && !out.includes(v)) out.push(v);
  }
  return out;
}

/** A label filter in the URL: `label=key=value`, repeatable and ANDed (the API's own grammar). */
export function labelFilters(search: URLSearchParams): string[] {
  return search.getAll("label").filter((l) => l.indexOf("=") > 0);
}

/**
 * A grant's state in the status vocabulary (DESIGN.md §6). Live is Active;
 * a window not yet open is Scheduled — information, not a fault; expired and
 * revoked are both deny-class, because neither backs a decision any more.
 */
export function grantStatus(state: GrantState | string): StatusName {
  switch (state) {
    case "active":
      return "active";
    case "scheduled":
      return "scheduled";
    case "expired":
      return "expired";
    case "revoked":
      return "revoked";
    default:
      return "notReported";
  }
}

export const GRANT_STATES: readonly GrantState[] = ["scheduled", "active", "expired", "revoked"];

/**
 * The field an `invalid_request` refusal names, so the form reports it at
 * that field (DESIGN.md §7). A body field is named as the API names it —
 * `hostname`, `scope.name`, `expires_at` — and anything else is the form's.
 */
export function refusedField(error: ApiError): string | null {
  if (error.code !== "invalid_request") return null;
  const field = error.parameters?.field;
  return typeof field === "string" && field ? field : null;
}

/** The sentence for a refused field's problem: the server's code, the console's words (M21). */
export const PROBLEMS: Readonly<Record<string, MessageKey>> = {
  required: "inventory.problem.required",
  invalid: "inventory.problem.invalid",
  too_long: "inventory.problem.tooLong",
  too_many: "inventory.problem.tooMany",
  ambiguous: "inventory.problem.ambiguous",
  not_after_start: "inventory.problem.notAfterStart",
  exceeds_maximum: "inventory.problem.exceedsMaximum",
};

export function problemKey(error: ApiError): MessageKey {
  const p = error.parameters?.problem;
  return (typeof p === "string" && PROBLEMS[p]) || "inventory.problem.other";
}

/**
 * What each fixed role is for (PLAN §6, internal/identity/rbac.go). A role
 * code is the product's, not an operator's, so its description is a
 * catalogue string; a code this console does not know is shown by its code
 * with the fallback, never hidden (test/inventory.test.ts holds this map to
 * the server's role set).
 */
export const ROLES: Readonly<Record<string, MessageKey>> = {
  integration: "inventory.role.integration",
  requester: "inventory.role.requester",
  auditor: "inventory.role.auditor",
  "policy-author": "inventory.role.policyAuthor",
  "grant-admin": "inventory.role.grantAdmin",
  "fleet-admin": "inventory.role.fleetAdmin",
  admin: "inventory.role.admin",
};

export function roleKey(role: string): MessageKey {
  return ROLES[role] ?? "inventory.role.unknown";
}

/** A binding's holder: whose it is, as a kind and an id. */
export function holderOf(b: RoleBinding): { kind: "subject" | "group"; id: string } {
  return b.group_id ? { kind: "group", id: b.group_id } : { kind: "subject", id: b.subject_id ?? "" };
}

/** A binding's stable key in a table: role, holder kind and holder id, the list's own order. */
export function bindingKey(b: RoleBinding): string {
  const h = holderOf(b);
  return `${b.role}/${h.kind}/${h.id}`;
}

/** The console's path to a holder's detail. */
export function holderPath(b: RoleBinding): string {
  const h = holderOf(b);
  return h.kind === "group" ? `inventory/groups/${encodeURIComponent(h.id)}` : `inventory/subjects/${encodeURIComponent(h.id)}`;
}
