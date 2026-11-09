// What the audit view decides from the URL and from a record, and nothing else
// (0051; PLAN M8, M19, M26, §7). Pure, so each rule is asserted directly
// (test/audit.test.ts).

import type { AuditRecord } from "../api/types";

/** The contract's record kinds: a closed set (PLAN §7, `audit.Kinds`). */
export const KINDS = [
  "session_start",
  "session_end",
  "auth",
  "authorize",
  "channel_open",
  "channel_close",
  "command",
  "policy_decision",
  "host_key",
  "stream",
  "provisioning",
  "error",
] as const;
export type Kind = (typeof KINDS)[number];

/** The contract's severities: a closed set. */
export const SEVERITIES = ["info", "warn", "critical"] as const;
export type Severity = (typeof SEVERITIES)[number];

/**
 * The record filters the URL carries, each the audit query's own parameter
 * (`GET …/audit/records`), so a filtered view under /console/ is the query
 * it shows (DESIGN.md §4). The time range is the picker's (TimeRange.tsx).
 */
export const RECORD_FILTERS = ["subject", "target", "session", "decision", "principal", "kind", "severity"] as const;
export type RecordFilter = (typeof RECORD_FILTERS)[number];
export type RecordFilters = Partial<Record<RecordFilter, string>>;

export function readRecordFilters(search: URLSearchParams): RecordFilters {
  const out: RecordFilters = {};
  for (const k of RECORD_FILTERS) {
    const v = search.get(k)?.trim();
    if (!v) continue;
    if (k === "kind" && !(KINDS as readonly string[]).includes(v)) continue;
    if (k === "severity" && !(SEVERITIES as readonly string[]).includes(v)) continue;
    out[k] = v;
  }
  return out;
}

export const DEFAULT_LIMIT = 100;
export const MAX_LIMIT = 10_000;

/** `?limit=` from the URL, within the API's own bounds (M26.1), else the screen's default. */
export function readLimit(search: URLSearchParams, fallback = DEFAULT_LIMIT): number {
  const n = Number(search.get("limit"));
  return Number.isInteger(n) && n >= 1 && n <= MAX_LIMIT ? n : fallback;
}

/** The API query for one page of records: the filters, the window, the order, the limit and the cursor. */
export function recordsQuery(
  filters: RecordFilters,
  window: { from?: string; to?: string },
  o: { limit: number; after?: string; ascending?: boolean },
): URLSearchParams {
  const q = new URLSearchParams();
  for (const k of RECORD_FILTERS) {
    const v = filters[k];
    if (v) q.set(k, v);
  }
  if (window.from) q.set("from", window.from);
  if (window.to) q.set("to", window.to);
  if (o.ascending) q.set("order", "asc");
  q.set("limit", String(o.limit));
  if (o.after) q.set("after", o.after);
  return q;
}

/**
 * Who acted, as the record says. `principal_id` is this server's attribution
 * of an act to a credential. `acting_operator` is a supervisor's WORD for the
 * person it acted for (PLAN M19, 0048): rendered beside the principal and
 * labelled as the supervisor's word, never as a subject, never linked, and
 * never filtered on. A record without it shows the principal alone. These are
 * keys of an open map: a key the console knows it renders, and one it does
 * not it skips (docs/API-VERSIONING.md).
 */
export function actorOf(r: AuditRecord): { principal?: string; acting?: string; supervisor: boolean } {
  const principal = r.attributes?.principal_id || undefined;
  const supervisor = Boolean(principal?.startsWith("sup-"));
  // The server writes the word only beside a supervisor's principal; a
  // record that carried it anywhere else is not one this console vouches for.
  const acting = supervisor ? r.attributes?.acting_operator || undefined : undefined;
  return { principal, acting, supervisor };
}

/** The attributes the record detail names, in this order; every other key is skipped there (the body shows all). */
export const KNOWN_ATTRIBUTES = [
  "event",
  "principal_id",
  "acting_operator",
  "actor_subject",
  "break_glass",
  "correlation_id",
  "before",
  "after",
  "command",
  "action",
] as const;

export function knownAttributes(r: AuditRecord): [string, string][] {
  const a = r.attributes ?? {};
  return KNOWN_ATTRIBUTES.filter((k) => a[k] !== undefined && a[k] !== "").map((k) => [k, a[k] as string]);
}

/** How many of the records on this page are of each kind, most first, then by name. */
export function countByKind(records: readonly AuditRecord[]): { kind: string; count: number }[] {
  const n = new Map<string, number>();
  for (const r of records) n.set(r.kind, (n.get(r.kind) ?? 0) + 1);
  return Array.from(n, ([kind, count]) => ({ kind, count })).sort((a, b) => b.count - a.count || a.kind.localeCompare(b.kind));
}

/** How many of the records on this page are of each severity, in the contract's order. */
export function countBySeverity(records: readonly AuditRecord[]): Record<Severity, number> {
  const out: Record<Severity, number> = { info: 0, warn: 0, critical: 0 };
  for (const r of records) if (r.severity in out) out[r.severity as Severity]++;
  return out;
}

/** A capture's payload, base64 as the API carries it, as bytes. */
export function captureBytes(payload: string): Uint8Array {
  const bin = atob(payload);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

// Escape sequences a pty stream carries that are a terminal's instructions,
// not text: CSI (colour, cursor movement, erase), OSC (a window title), and
// the two-byte escapes.
const ESCAPES = /\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]/g;

/**
 * A session's capture as the text a reader sees: the chunks joined as bytes
 * first (a character can be split across two reads), decoded as UTF-8, its
 * escape sequences dropped, and carriage return, backspace and newline
 * applied as a terminal applies them — a progress bar redrawn on one line
 * reads as its last state. Every other control character is dropped; a tab
 * is kept. It is a reading of the bytes, never a substitute for them: the
 * record's digest covers the bytes, and the API serves them.
 */
export function terminalText(chunks: readonly Uint8Array[]): string {
  const size = chunks.reduce((n, c) => n + c.length, 0);
  const all = new Uint8Array(size);
  let at = 0;
  for (const c of chunks) {
    all.set(c, at);
    at += c.length;
  }
  const text = new TextDecoder("utf-8", { fatal: false }).decode(all).replace(ESCAPES, "");
  const lines: string[] = [];
  let line: string[] = [];
  let cursor = 0;
  for (const ch of text) {
    if (ch === "\n") {
      lines.push(line.join(""));
      line = [];
      cursor = 0;
    } else if (ch === "\r") {
      cursor = 0;
    } else if (ch === "\b") {
      cursor = Math.max(0, cursor - 1);
    } else if (ch === "\t" || ch >= " ") {
      if (ch === "\x7f") continue;
      line[cursor] = ch;
      cursor++;
    }
  }
  lines.push(line.join(""));
  return lines.join("\n");
}
