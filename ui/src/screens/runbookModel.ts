// THE EMERGENCY RUNBOOK, AS DATA (0058; PLAN §5.2; upstream api/README.md,
// "Banned algorithms", which states the steps and is cited, never restated).
// Pure, and unit-tested in test/runbook.test.ts.
//
// Three steps, in order, each a call 0019 serves and audits:
//   1. the ban, added to every route and activated (POST …/policy/apply);
//   2. `cache_invalidate` with `all` (POST …/revocations), because a cached
//      decision keeps the old policy until its hint runs out;
//   3. the running sessions found by what they negotiated (GET
//      …/audit/negotiated, read to its last page) and each one ended with
//      `session_kill` (POST …/revocations), its `reason` shown to the user.
// A step is offered only once the one before it has answered, so the
// workflow cannot reach the third step — or finish — without the second: a
// ban that stops at activation reaches only new decisions.

import type { AlgorithmAxis, NegotiatedSession, RevocationReceipt } from "../api/types";

/**
 * The axes a session's records say it negotiated (audit.negotiatedAxes). The
 * public-key list is what the proxy OFFERED, not a negotiated value, so it is
 * not a runbook axis: a ban there is authored in the policy itself.
 */
export const NEGOTIATED_AXES: readonly AlgorithmAxis[] = ["key_exchanges", "ciphers", "macs", "host_keys"];

/** How many session ids one `session_kill` carries: well inside a request body's limit. */
export const KILL_BATCH = 500;

/** An SSH algorithm identifier, as the contract spells one: no spaces, no YAML indicator first. */
export function validIdentifier(name: string): boolean {
  return /^[A-Za-z0-9][A-Za-z0-9@._+-]{0,127}$/.test(name);
}

export interface Banned {
  /** The candidate source: the active one with the ban added, or unchanged. */
  source: string;
  /** The active source already bans the name everywhere: step 1 is already in force. */
  already: boolean;
  /**
   * False when the bundle's own `algorithm_bans` is written in a shape this
   * console does not edit (a flow mapping, an anchor): the operator adds the
   * name by hand, in the editor, before checking it.
   */
  edited: boolean;
}

const TOP = /^algorithm_bans:\s*(#.*)?$/;
const FLOW_LIST = /^(\s+)([a-z_]+):\s*\[(.*)\]\s*(#.*)?$/;
const BLOCK_KEY = /^(\s+)([a-z_]+):\s*(#.*)?$/;
const ITEM = /^(\s+)-\s+(\S+)\s*(#.*)?$/;

function unquote(s: string): string {
  const t = s.trim();
  return (t.startsWith('"') && t.endsWith('"')) || (t.startsWith("'") && t.endsWith("'")) ? t.slice(1, -1) : t;
}

/**
 * The ban EVERYWHERE, written as the bundle's own top-level `algorithm_bans`
 * — which the compiler adds to every allow route's (PLAN §5.2). Text in, text
 * out: the operator reviews the result in the editor and the server compiles
 * it, so nothing here decides whether it is valid.
 */
export function withBan(source: string, axis: AlgorithmAxis, name: string): Banned {
  const lines = source.replace(/\r\n/g, "\n").split("\n");
  const top = lines.findIndex((l) => TOP.test(l));
  if (top < 0) {
    if (lines.some((l) => /^algorithm_bans:/.test(l))) return { source, already: false, edited: false };
    const body = source.endsWith("\n") || source === "" ? source : `${source}\n`;
    return { source: `${body}\nalgorithm_bans:\n  ${axis}: [${name}]\n`, already: false, edited: true };
  }
  // The block is every line after it that is indented, blank or a comment.
  let end = top + 1;
  while (end < lines.length && (/^\s/.test(lines[end] ?? "") || (lines[end] ?? "").trim() === "" || /^\s*#/.test(lines[end] ?? ""))) end++;
  while (end > top + 1 && (lines[end - 1] ?? "").trim() === "") end--;
  let indent = "  ";
  for (let i = top + 1; i < end; i++) {
    const line = lines[i] ?? "";
    const flow = FLOW_LIST.exec(line);
    const key = flow ? null : BLOCK_KEY.exec(line);
    const m = flow ?? key;
    if (!m) continue;
    indent = m[1] ?? indent;
    if (m[2] !== axis) continue;
    if (flow) {
      const names = (flow[3] ?? "").split(",").map(unquote).filter(Boolean);
      if (names.includes(name)) return { source, already: true, edited: false };
      const next = [...names, name].join(", ");
      const out = [...lines];
      out[i] = `${flow[1]}${axis}: [${next}]${flow[4] ? ` ${flow[4]}` : ""}`;
      return { source: out.join("\n"), already: false, edited: true };
    }
    // A block list beneath the key.
    let last = i;
    let itemIndent = `${indent}  `;
    for (let j = i + 1; j < end; j++) {
      const it = ITEM.exec(lines[j] ?? "");
      if (!it || (it[1] ?? "").length <= (m[1] ?? "").length) break;
      if (unquote(it[2] ?? "") === name) return { source, already: true, edited: false };
      itemIndent = it[1] ?? itemIndent;
      last = j;
    }
    if (last === i && lines[i + 1] !== undefined && !ITEM.test(lines[i + 1] ?? "") && /^\s/.test(lines[i + 1] ?? "")) {
      return { source, already: false, edited: false };
    }
    const out = [...lines];
    out.splice(last + 1, 0, `${itemIndent}- ${name}`);
    return { source: out.join("\n"), already: false, edited: true };
  }
  const out = [...lines];
  out.splice(top + 1, 0, `${indent}${axis}: [${name}]`);
  return { source: out.join("\n"), already: false, edited: true };
}

/** Splits the sessions into the batches each `session_kill` carries. */
export function batches<T>(xs: readonly T[], size = KILL_BATCH): T[][] {
  const out: T[][] = [];
  for (let i = 0; i < xs.length; i += size) out.push(xs.slice(i, i + size));
  return out;
}

export interface Progress {
  /** Step 1: the version the ban is active in, applied here or already in force. */
  ban?: { version: number; via: "applied" | "active" };
  /** Step 2: what `cache_invalidate` with `all` reported covering. */
  invalidated?: RevocationReceipt;
  /** Step 3: the sessions every page of the negotiated list named, and how many pages that was. */
  found?: { sessions: NegotiatedSession[]; pages: number };
  /** Step 3: each `session_kill`'s receipt, and the sessions those calls ended. */
  killed: { receipts: RevocationReceipt[]; ended: string[] };
}

export const START: Progress = { killed: { receipts: [], ended: [] } };

export type StepState = "done" | "ready" | "waiting";

/** Where each step stands: a step is ready only once the one before it is done. */
export function stepStates(p: Progress): [StepState, StepState, StepState] {
  const one: StepState = p.ban ? "done" : "ready";
  const two: StepState = p.invalidated ? "done" : one === "done" ? "ready" : "waiting";
  const ended = new Set(p.killed.ended);
  const three: StepState =
    two !== "done" ? "waiting" : p.found && p.found.sessions.every((s) => ended.has(s.session_id)) ? "done" : "ready";
  return [one, two, three];
}

/** The sessions still to end: listed, and not yet in an answered `session_kill`. */
export function remaining(p: Progress): string[] {
  const ended = new Set(p.killed.ended);
  return (p.found?.sessions ?? []).map((s) => s.session_id).filter((id) => !ended.has(id));
}

/** Whether the runbook is complete: all three steps answered, the second among them. */
export function finished(p: Progress): boolean {
  return stepStates(p).every((s) => s === "done");
}
