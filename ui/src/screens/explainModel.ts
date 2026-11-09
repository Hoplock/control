// What Explain decides from a story, and nothing else (0051; PLAN M4, M11,
// M28, §5.6). Pure, so each rule is asserted directly (test/explain.test.ts).

import type { StatusName } from "../components/Status";
import type { Story } from "../api/types";

/**
 * A decision's effect in the status vocabulary. A deny is a decision this
 * server made; `unserved` is an allow it could not answer — an outage, never a
 * deny (M11) — so the two never render alike.
 */
export function effectStatus(effect: string): StatusName {
  switch (effect) {
    case "allow":
      return "allowed";
    case "deny":
      return "denied";
    case "unserved":
      return "unserved";
    default:
      return "notReported";
  }
}

/** Why a decision came out as it did: what the story's outcome section says first. */
export type Basis = "rule" | "default" | "standing" | "unserved";

export function basis(s: Story): Basis {
  if (s.effect === "unserved") return "unserved";
  if (s.standing?.length || s.explanation.standing?.length) return "standing";
  if (s.rule) return "rule";
  // No rule answered: the bundle's default deny, which is itself the answer.
  return "default";
}

/** How far along its hops a decision was taken: the proxies before it. */
function depth(s: Story): number {
  return s.inputs.context.hop_trail?.length ?? 0;
}

/**
 * A session's decisions in the order the session met them: hop by hop, the
 * edge first, and by instant within a hop. The API answers newest first.
 */
export function inHopOrder(stories: readonly Story[]): Story[] {
  return [...stories].sort((a, b) => depth(a) - depth(b) || Date.parse(a.decided_at) - Date.parse(b.decided_at));
}

/**
 * What a session came to: a deny anywhere on its path denies it; otherwise an
 * unserved hop is an outage; otherwise it was allowed.
 */
export function sessionOutcome(stories: readonly Story[]): StatusName {
  if (stories.some((s) => s.effect === "deny")) return "denied";
  if (stories.some((s) => s.effect === "unserved")) return "unserved";
  if (stories.length && stories.every((s) => s.effect === "allow")) return "allowed";
  return "notReported";
}

/** The decision a session opens on: its first deny, else its first outage, else its innermost hop. */
export function openingDecision(stories: readonly Story[]): string | null {
  const ordered = inHopOrder(stories);
  return (
    ordered.find((s) => s.effect === "deny")?.decision_id ??
    ordered.find((s) => s.effect === "unserved")?.decision_id ??
    ordered[ordered.length - 1]?.decision_id ??
    null
  );
}

/** One stop on a hop path: a proxy (and the decision it took, if this server holds it), or the target. */
export interface Hop {
  kind: "proxy" | "target";
  id: string;
  story?: Story;
  /** A proxy the route names next, which took no decision this session holds. */
  next?: boolean;
}

/**
 * The path a session took, from the edge proxy to the target: every proxy a
 * decision's hop trail names, each with the decision it took where this
 * server holds one, the proxy the innermost decision routed to next if it
 * took none here, and the target last.
 */
export function hopPath(stories: readonly Story[]): Hop[] {
  const ordered = inHopOrder(stories);
  const byProxy = new Map<string, Story>();
  for (const s of ordered) {
    const proxy = s.inputs.context.proxy_id || s.proxy_id;
    if (proxy && !byProxy.has(proxy)) byProxy.set(proxy, s);
  }
  const path: string[] = [];
  const add = (p: string | undefined) => {
    if (p && !path.includes(p)) path.push(p);
  };
  for (const s of ordered) {
    for (const p of s.inputs.context.hop_trail ?? []) add(p);
    add(s.inputs.context.proxy_id || s.proxy_id);
  }
  const last = ordered[ordered.length - 1];
  const hops: Hop[] = path.map((p) => ({ kind: "proxy", id: p, story: byProxy.get(p) }));
  const next = last?.explanation.next_proxy_id || last?.snapshot?.hop?.next_proxy_id;
  if (next && !path.includes(next)) hops.push({ kind: "proxy", id: next, next: true });
  const target = last?.snapshot?.hop?.final_target || last?.snapshot?.target || last?.inputs.target.hostname;
  if (target) hops.push({ kind: "target", id: target });
  return hops;
}

/**
 * Where an id pasted into Explain is looked up first. A decision id is minted
 * here and always begins `d_`; anything else is what a user was told — a
 * session id, chosen by the proxy.
 */
export function lookupOf(id: string): "decisions" | "sessions" {
  return id.startsWith("d_") ? "decisions" : "sessions";
}
