// What the audit view decides about a stream's holes and its retention, and
// nothing else (0057; PLAN M8, M11, §7). Pure, so each rule is asserted
// directly (test/gaps.test.ts).
//
// A `logging.gap` is a proxy's own report of records this server does not
// hold (`Hoplock/proxy#71`; upstream `api/README.md`, "When records do not
// arrive"). Its place is its SPAN — when the hole was — and never its own
// timestamp, which is when the proxy wrote the report, often after the
// session ended. Its cause is one of two different facts:
//   - `evicted`: this server never received the records; the proxy was cut
//     off for longer than its disk window held them. Outage-class (M11).
//   - `refused`: this server received them and refused them itself, and the
//     report repeats its answer. Neither a deny nor an outage.
// A gap is never tampering, and a chain that verifies is never "nothing
// missing": the chain says nothing about a record that never arrived.

import type { AuditRecord, Gap, GapLoss } from "../api/types";
import type { StatusName } from "../components/Status";

/** The event a gap report carries (`kind: error`). */
export const GAP_EVENT = "logging.gap";
export { RETAINED_EVENT } from "../components/FeedLag";
/** The two causes upstream defines. */
export const GAP_CAUSES = ["evicted", "refused"] as const;
export type GapCause = (typeof GAP_CAUSES)[number];

export function isGapRecord(r: AuditRecord): boolean {
  return (r.event || r.attributes?.event) === GAP_EVENT;
}

/** How a gap reads, by its cause: a cause this console does not know is not reported, never one of the two. */
export function gapStatus(cause: string): StatusName {
  switch (cause) {
    case "evicted":
      return "evicted";
    case "refused":
      return "refusedAtIngest";
    default:
      return "notReported";
  }
}

/** A gap that hid something critical: a blocked command absent from this store is itself a security fact. */
export function gapCritical(g: Gap): boolean {
  return (g.gap_critical ?? 0) > 0 || g.severity === "critical";
}

function count(v: string | undefined): number | undefined {
  if (v === undefined || v.trim() === "") return undefined;
  const n = Number(v);
  return Number.isInteger(n) && n >= 0 ? n : undefined;
}

function list(v: string | undefined): string[] | undefined {
  const out = (v ?? "")
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);
  return out.length ? out : undefined;
}

/**
 * A gap read from its record's attributes, leniently, as the server reads it
 * (internal/audit gapFacts): a key that is absent or does not parse is
 * absent, never zero. The record view is where the audit trail shows one.
 */
export function gapOfRecord(r: AuditRecord): Gap {
  const a = r.attributes ?? {};
  return {
    record_id: r.record_id,
    stream: r.stream,
    chain_seq: r.chain_seq,
    session_id: r.session_id || undefined,
    proxy_id: r.proxy_id,
    severity: r.severity,
    gap_cause: a.gap_cause ?? "",
    gap_records: count(a.gap_records),
    gap_bytes: count(a.gap_bytes),
    gap_first_at: a.gap_first_at || undefined,
    gap_last_at: a.gap_last_at || undefined,
    gap_kinds: list(a.gap_kinds),
    gap_critical: count(a.gap_critical),
    gap_record_ids: list(a.gap_record_ids),
    gap_record_ids_truncated: a.gap_record_ids_truncated === "true" || undefined,
    refusal_code: a.refusal_code || undefined,
    refusal_message: a.refusal_message || undefined,
    reported_at: r.recorded_at,
  };
}

/** One step of a session read with its holes: a record at its instant, or a gap at the start of its span. */
export type Entry = { kind: "record"; id: string; record: AuditRecord } | { kind: "gap"; id: string; gap: Gap };

const ms = (at: string | undefined) => (at ? Date.parse(at) : Number.NaN);

/**
 * A page of a session's records and the session's gaps, in one order. Each
 * record is at its instant and each gap at the start of its span; at one
 * instant the records come first, because they are what arrived. A gap
 * report's own record is not a step — its place is its span — and a gap with
 * no span (no missing record carried a timestamp) is not placed: it is
 * returned apart, to be listed beside the timeline rather than put at a time
 * it does not have.
 *
 * `window` bounds the page: a gap is placed on it only when its span starts
 * within it, so paging a long session never piles every gap onto one page.
 * An open side is unbounded.
 */
export function placeGaps(
  records: readonly AuditRecord[],
  gaps: readonly Gap[],
  window: { from?: string; to?: string } = {},
): { entries: Entry[]; unplaced: Gap[] } {
  const from = ms(window.from);
  const to = ms(window.to);
  const steps = records.filter((r) => !isGapRecord(r));
  const unplaced: Gap[] = [];
  const placed: Gap[] = [];
  for (const g of gaps) {
    const at = ms(g.gap_first_at);
    if (Number.isNaN(at)) {
      unplaced.push(g);
      continue;
    }
    if (!Number.isNaN(from) && at < from) continue;
    if (!Number.isNaN(to) && at > to) continue;
    placed.push(g);
  }
  placed.sort((a, b) => ms(a.gap_first_at) - ms(b.gap_first_at) || a.chain_seq - b.chain_seq);
  const entries: Entry[] = [];
  let g = 0;
  for (const r of steps) {
    const at = ms(r.recorded_at);
    while (g < placed.length && ms((placed[g] as Gap).gap_first_at) < at) {
      const gap = placed[g++] as Gap;
      entries.push({ kind: "gap", id: gap.record_id, gap });
    }
    entries.push({ kind: "record", id: r.record_id, record: r });
  }
  for (; g < placed.length; g++) {
    const gap = placed[g] as Gap;
    entries.push({ kind: "gap", id: gap.record_id, gap });
  }
  return { entries, unplaced };
}

/**
 * A session's loss, one row per cause, in upstream's order and then any other
 * cause the server summed. Two reports of one cause count disjoint records,
 * so a cause's loss is their SUM, which the server computes
 * (`…/audit/sessions/{session}/loss`); the console never takes the latest
 * report as the whole. `critical` is summed from the session's reports the
 * console holds, and is absent when it does not hold them all.
 */
export function lossByCause(
  loss: readonly GapLoss[],
  gaps: readonly Gap[] | null,
): { cause: string; reports: number; records: number; bytes: number; critical?: number }[] {
  const rank = (c: string) => {
    const i = (GAP_CAUSES as readonly string[]).indexOf(c);
    return i < 0 ? GAP_CAUSES.length : i;
  };
  return [...loss]
    .sort((a, b) => rank(a.cause) - rank(b.cause) || a.cause.localeCompare(b.cause))
    .map((l) => {
      if (!gaps) return { ...l };
      const of = gaps.filter((g) => g.gap_cause === l.cause);
      return { ...l, critical: of.reduce((n, g) => n + (g.gap_critical ?? 0), 0) };
    });
}

/** The gaps of a page, by the stream they were reported on (one per proxy), each placed by its span. */
export function gapsByStream(gaps: readonly Gap[]): { stream: string; proxy?: string; placed: Gap[]; unplaced: Gap[] }[] {
  const by = new Map<string, Gap[]>();
  for (const g of gaps) by.set(g.stream, [...(by.get(g.stream) ?? []), g]);
  return Array.from(by, ([stream, of]) => {
    const placed = of.filter((g) => !Number.isNaN(ms(g.gap_first_at)));
    placed.sort((a, b) => ms(a.gap_first_at) - ms(b.gap_first_at) || a.chain_seq - b.chain_seq);
    return { stream, proxy: of.find((g) => g.proxy_id)?.proxy_id, placed, unplaced: of.filter((g) => Number.isNaN(ms(g.gap_first_at))) };
  }).sort((a, b) => a.stream.localeCompare(b.stream));
}

/** What a retention pass's record says, from its operator attributes (internal/audit retention.go). */
export interface RetainedFacts {
  stream?: string;
  through?: number;
  deleted?: number;
  /** Present only when an archive was registered when the pass ran. */
  heldByArchive?: boolean;
  archivedThrough?: number;
}

export function retainedOf(r: AuditRecord): RetainedFacts {
  const a = r.attributes ?? {};
  const held = a["operator.held_by_archive"];
  return {
    stream: a["operator.stream"] || undefined,
    through: count(a.after),
    deleted: count(a["operator.deleted"]),
    heldByArchive: held === "true" ? true : held === "false" ? false : undefined,
    archivedThrough: count(a["operator.archived_through_seq"]),
  };
}
