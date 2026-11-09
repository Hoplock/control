// POLICY'S RULES (0053; PLAN §5.6, M21, M26). Pure: what the Policy screens
// read out of an answer — which status a version and a finding are, which
// warnings still gate an activation, which values a code's sentence is built
// from and which lists a finding names beside it — kept apart from the
// screens so each rule is a unit test.

import type { StatusName } from "../components/Status";
import type { Finding, PolicyVersion, Rejection } from "../api/types";
import type { Values } from "../i18n/messages";

/** A version as the console names it: the identifier an author types to confirm activating it. */
export function versionName(version: number): string {
  return `v${version}`;
}

/** Reads a `{version}` path value; anything but a positive integer is none. */
export function readVersion(raw: string | undefined): number | null {
  const n = Number(raw);
  return Number.isInteger(n) && n > 0 ? n : null;
}

/** The one active version, superseded is every other (PLAN §5.6). */
export function versionStatus(v: Pick<PolicyVersion, "active">): StatusName {
  return v.active ? "active" : "superseded";
}

/** Activating an older version than the active one is a rollback (§5.6). */
export function isRollback(version: number, active: number | null): boolean {
  return active !== null && version < active;
}

/** A finding's severity in the status vocabulary (DESIGN.md §6). */
export function severityStatus(severity: string): StatusName {
  switch (severity) {
    case "blocking":
      return "blocking";
    case "warning":
      return "warning";
    case "advice":
      return "advice";
    default:
      // The server answers an outage for a severity outside the three
      // (0022), so this is a newer server's word; it never reads as fine.
      return "notReported";
  }
}

const SEVERITY_ORDER: Record<string, number> = { blocking: 0, warning: 1, advice: 2 };

/** The report's own order: by severity, then id (§5.6), whoever produced them. */
export function inReportOrder(findings: readonly Finding[]): Finding[] {
  return [...findings].sort(
    (a, b) => (SEVERITY_ORDER[a.severity] ?? 3) - (SEVERITY_ORDER[b.severity] ?? 3) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0),
  );
}

/** How many findings of each severity a report holds. */
export function severityCounts(findings: readonly Finding[]): { blocking: number; warning: number; advice: number } {
  const out = { blocking: 0, warning: 0, advice: 0 };
  for (const f of findings) {
    if (f.severity === "blocking" || f.severity === "warning" || f.severity === "advice") out[f.severity]++;
  }
  return out;
}

/** The warnings an activation must accept by id, and has not. */
export function unaccepted(findings: readonly Finding[], accepted: ReadonlySet<string>): Finding[] {
  return findings.filter((f) => f.severity === "warning" && !accepted.has(f.id));
}

/**
 * Merges what a refusal names into what the report showed: a `409
 * findings_not_accepted` lists each warning as the server computed it at
 * activation, against the fleet as it is then (§5.6), which may differ.
 */
export function mergeFindings(shown: readonly Finding[], named: readonly Finding[]): Finding[] {
  const ids = new Set(shown.map((f) => f.id));
  return [...shown, ...named.filter((f) => !ids.has(f.id))];
}

/** The findings a refusal's parameters carry, read defensively. */
export function findingsOf(parameters: Record<string, unknown> | undefined): Finding[] {
  const raw = parameters?.findings;
  if (!Array.isArray(raw)) return [];
  return raw.filter((f): f is Finding => typeof f === "object" && f !== null && typeof (f as Finding).id === "string");
}

/** The rejections a `422 policy_invalid` carries, read defensively. */
export function rejectionsOf(parameters: Record<string, unknown> | undefined): Rejection[] {
  const raw = parameters?.rejections;
  if (!Array.isArray(raw)) return [];
  return raw.filter((r): r is Rejection => typeof r === "object" && r !== null && typeof (r as Rejection).code === "string");
}

/** First-strong isolates, so an identifier inside a sentence keeps its own direction (DESIGN.md §9). */
function isolate(v: string): string {
  return `\u2068${v}\u2069`;
}

/** A rejection's values: its parameters, each an identifier set apart. */
export function rejectionValues(r: Rejection): Values {
  const out: Values = {};
  for (const [k, v] of Object.entries(r.parameters ?? {})) out[k] = isolate(String(v));
  return out;
}

/** The lists a finding may name beside its sentence (§5.6, 0035): at most 100, with the whole number. */
const LISTS = ["targets", "proxies", "rules", "terms"] as const;

/**
 * A finding's values: its scalar parameters (identifiers isolated), a nested
 * record's fields as `<name>_<field>` (algorithm.policy_unmet's `unmet`), and
 * the count of each list it names, as `<list>_count` — zero when it names
 * none, so a sentence that counts can say so.
 */
export function findingValues(f: Finding): Values {
  const out: Values = { targets_count: 0, proxies_count: 0, source: isolate(f.source), code: isolate(f.code) };
  for (const [k, v] of Object.entries(f.parameters ?? {})) {
    if (typeof v === "string") out[k] = isolate(v);
    else if (typeof v === "number") out[k] = v;
    else if (typeof v === "boolean") out[k] = String(v);
    else if (v && typeof v === "object" && !Array.isArray(v)) {
      for (const [field, x] of Object.entries(v as Record<string, unknown>)) {
        if (typeof x === "string") out[`${k}_${field}`] = isolate(x);
        else if (typeof x === "number") out[`${k}_${field}`] = x;
      }
    }
  }
  for (const name of LISTS) {
    const list = f.parameters?.[name];
    if (Array.isArray(list) && typeof f.parameters?.[`${name}_count`] !== "number") out[`${name}_count`] = list.length;
  }
  return out;
}

/** One item of a finding's list: what it names, and what it lacks or offers. */
export interface ListItem {
  name: string;
  missing: string[];
  offered: string[];
}

export interface FindingList {
  name: (typeof LISTS)[number];
  items: ListItem[];
  /** The whole number, of which `items` are at most the first 100. */
  count: number;
  truncated: boolean;
}

function itemOf(x: unknown): ListItem {
  if (typeof x === "string") return { name: x, missing: [], offered: [] };
  if (x && typeof x === "object") {
    const o = x as Record<string, unknown>;
    const name = [o.proxy_id, o.hostname, o.target, o.name].find((v) => typeof v === "string") as string | undefined;
    const strings = (v: unknown) => (Array.isArray(v) ? v.filter((s): s is string => typeof s === "string") : []);
    return { name: name ?? JSON.stringify(x), missing: strings(o.missing), offered: strings(o.offered) };
  }
  return { name: String(x), missing: [], offered: [] };
}

/** The lists a finding names, each with its whole number and whether it was cut (0035). */
export function findingLists(f: Finding): FindingList[] {
  const out: FindingList[] = [];
  for (const name of LISTS) {
    const raw = f.parameters?.[name];
    if (!Array.isArray(raw) || raw.length === 0) continue;
    const count = f.parameters?.[`${name}_count`];
    const truncated = f.parameters?.[`${name}_truncated`];
    out.push({
      name,
      items: raw.map(itemOf),
      count: typeof count === "number" ? count : raw.length,
      truncated: truncated === true,
    });
  }
  return out;
}

/** The replay window a range picker's choice asks for: replay needs both ends. */
export function replayWindow(resolved: { from?: string; to?: string }, now: string): { from: string; to: string } {
  return { from: resolved.from ?? "1970-01-01T00:00:00Z", to: resolved.to ?? now };
}
