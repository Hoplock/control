// THE VISUAL VIEW'S RULES (0066; PLAN §5.3, §5.6). Pure: which rejection and
// finding sits on which rule's card, what a rule's match reads as chip by
// chip, and which card and which chips a trace marks. Kept apart from the
// screen so each rule is a unit test.

import type { DryRunResult, Finding, Rejection } from "../api/types";
import type { StatusName } from "../components/Status";
import { ruleAtLine, type MatchTerm, type RuleView } from "./policyDocument";
import { MATCH_AXES } from "./policyVocabulary";

export interface Notes {
  rejections: Rejection[];
  findings: Finding[];
}

/**
 * Each note on the card of the rule it names — by id, or else by the line it
 * is at — and the rest heading the list, at document scope. A rule id named
 * twice (`rule.id_duplicate`) is placed by its line, which tells them apart.
 */
export function notesByRule(
  rules: readonly RuleView[],
  rejections: readonly Rejection[],
  findings: readonly Finding[],
): { byRule: Map<number, Notes>; document: Notes } {
  const byRule = new Map<number, Notes>();
  const document: Notes = { rejections: [], findings: [] };
  const place = (rule: string | undefined, line: number | undefined): Notes => {
    const atLine = ruleAtLine(rules, line);
    const named = rule ? rules.filter((r) => r.id === rule) : [];
    const target = atLine && (!rule || atLine.id === rule) ? atLine : named.length === 1 ? named[0] : atLine;
    if (!target) return document;
    let n = byRule.get(target.index);
    if (!n) byRule.set(target.index, (n = { rejections: [], findings: [] }));
    return n;
  };
  for (const r of rejections) place(r.rule, r.line).rejections.push(r);
  for (const f of findings) place(f.rule, f.line).findings.push(f);
  return { byRule, document };
}

/** A rule's effect in the status vocabulary (DESIGN.md §6): allowed, denied, or not reported. */
export function effectStatus(effect: string | undefined): StatusName {
  return effect === "allow" ? "allowed" : effect === "deny" ? "denied" : "notReported";
}

/** One chip of a rule's match: a term and one value it names. */
export interface Chip {
  axis: string;
  term: string;
  value: string;
  /** How the engine names it in a decision's matched terms (`subject.groups=sre`). */
  key: string;
}

// The engine names a matched term by the input it read, which is the
// singular of the bundle's key for most terms (model.MatchedTerm.Term).
const ENGINE_TERM: Readonly<Record<string, string>> = {
  "subject.ids": "id",
  "subject.sources": "source",
  "subject.auth_methods": "auth_method",
  "context.days": "day",
  "context.source_cidrs": "source_network",
  "context.proxy_ids": "proxy_id",
  "target.hostnames": "hostname",
  "target.zones": "zone",
  "grant.scopes": "scope",
  "grant.origins": "origin",
  "device.required": "present",
};

function engineTerm(axis: string, term: string): string {
  return ENGINE_TERM[`${axis}.${term}`] ?? term;
}

/** A rule's match, axis by axis in the order a match is read, a chip per value. */
export function chipsByAxis(match: readonly MatchTerm[]): { axis: string; chips: Chip[] }[] {
  const out: { axis: string; chips: Chip[] }[] = [];
  for (const axis of MATCH_AXES) {
    const chips = match
      .filter((m) => m.axis === axis)
      .flatMap((m) => m.values.map((value) => ({ axis, term: m.term, value, key: `${axis}.${engineTerm(axis, m.term)}=${engineValue(axis, m.term, value)}` })));
    if (chips.length) out.push({ axis, chips });
  }
  return out;
}

function engineValue(axis: string, term: string, value: string): string {
  // The engine records a day by its English weekday name, as Go's time.Weekday prints it.
  if (axis === "context" && term === "days") return value.charAt(0).toUpperCase() + value.slice(1);
  return value;
}

/** Where a trace landed: a rule's card, or the default deny, and the chips that matched. */
export interface TraceMark {
  rule: number | "default";
  matched: ReadonlySet<string>;
}

export function traceMark(rules: readonly RuleView[], result: DryRunResult): TraceMark {
  const matched = new Set(result.terms ?? []);
  if (!result.rule) return { rule: "default", matched };
  const byLine = result.rule_line ? rules.find((r) => r.line === result.rule_line && r.id === result.rule) : undefined;
  const rule = byLine ?? rules.find((r) => r.id === result.rule);
  return { rule: rule ? rule.index : "default", matched };
}

/** A route as one line: intent, channels, request types, the ladder's methods, the rungs. */
export function routeSummary(rule: RuleView): string[] {
  const r = rule.route;
  if (!r) return [];
  return [
    r.intent ?? "",
    r.channels.join(", "),
    r.requestTypes.join(", "),
    r.credentials.map((c) => c.method ?? "").filter(Boolean).join(" → "),
    r.execution ?? "",
    r.reach ?? "",
  ].filter((s) => s !== "");
}
