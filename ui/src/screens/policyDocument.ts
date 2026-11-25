// A POLICY SOURCE AS A DOCUMENT (0066; PLAN M3, §5.6). The visual policy
// editor works on the source an author wrote, never on a copy re-serialised
// from a model of it: a stored version IS its source and that source's digest
// (§5.6), and a reviewer reads the diff. So the source text is the one truth,
// and this module does three things with it:
//
//   - reads it (the `yaml` library's document model, with each node's place in
//     the text) into what the visual view shows: the rules in the order they
//     are tried, each one's match, effect and route, and the bundle's own
//     label and group declarations;
//   - writes it back unchanged: `serialise` is the parser's concrete syntax
//     tree stringified, which holds every byte — comments, blank lines,
//     quoting, key order — so a round trip is byte-identical by construction,
//     and a test holds it to that over every fixture;
//   - edits it as the smallest change to the text that says the edit: a value
//     replaced where it stands, a key inserted after its last sibling, a rule
//     moved as its own lines (its comments with it). Nothing it did not touch
//     changes, and an edit that would lose a comment is refused rather than
//     made (`comment_lost`).
//
// What the view does not model it leaves alone and lists (`unmodelled`), so
// the screen can show it read-only with "edit in source" — it is never
// dropped. Pure: no React, no DOM; every rule here is a unit test.

import { isMap, isPair, isScalar, isSeq, type YAMLMap } from "yaml";
import {
  appendItem,
  attempt,
  blockScalar,
  blockSeq,
  column,
  commentsOf,
  flowScalar,
  itemSpan,
  joinLayout,
  keyOf,
  layoutOf,
  lineEnd,
  lineOf,
  moveItem,
  nodeAt,
  parseYaml,
  range,
  Refused,
  removeItem,
  removeSpan,
  reparse,
  serialise,
  setAt,
  spanComments,
  splice,
  startOf,
  str,
  strings,
  withNewline,
  type Applied,
  type EditRefusal,
  type Path,
  type PathKey,
  type Value,
  type YamlDocument,
} from "./yamlDocument";

export { commentsOf, serialise, type Applied, type EditRefusal, type Path, type PathKey, type Value };

/** A policy source, parsed (yamlDocument.ts): a rule is never emptied away by a field's removal. */
export type PolicyDocument = YamlDocument;

export function parsePolicy(source: string): PolicyDocument {
  return parseYaml(source, isRuleField);
}

/** A rule's own map: `["rules", n]`. */
function isRuleField(path: Path): boolean {
  return path.length === 2 && path[0] === "rules";
}

// ---------------------------------------------------------------------------
// Reading: what the visual view shows.
// ---------------------------------------------------------------------------

/** One term of a match: an axis, the term's key under it, and what it names. */
export interface MatchTerm {
  axis: string;
  /** The key under the axis (`groups`, `labels.env`, `days`). */
  term: string;
  values: string[];
  /** Whether the editor can change it (false: shown read-only, "edit in source"). */
  modelled: boolean;
}

export interface Credential {
  method?: string;
  /** `literal` when the username is a plain string, else its `from`. */
  usernameSource?: string;
  usernameLiteral?: string;
  /** The entry's other keys, which the editor shows read-only. */
  other: string[];
}

export interface RouteView {
  intent?: string;
  target?: string;
  channels: string[];
  requestTypes: string[];
  filterMode?: string;
  execMode?: string;
  credentials: Credential[];
  execution?: string;
  reach?: string;
  algorithmProfile?: string;
  algorithmFloor?: string;
  requireSessionCapture?: boolean;
}

/** A part of the schema the editor does not model, with where it is. */
export interface Unmodelled {
  /** Its path, dotted (`route.forwards`). */
  path: string;
  line: number;
}

export interface RuleView {
  index: number;
  id: string;
  /** The line the compiler reports the rule at (its `- id:` line), 1-based. */
  line: number;
  /** Its last line, 1-based: a note at a line in [line, endLine] is about it. */
  endLine: number;
  description?: string;
  effect?: string;
  reason?: string;
  match: MatchTerm[];
  route?: RouteView;
  /** `route:` present but not a mapping, or a deny's route: shown read-only. */
  routePresent: boolean;
  obligations: string[];
  cache?: { key: string[]; ttl?: number };
  unmodelled: Unmodelled[];
}

export interface LabelDecl {
  key: string;
  values: string[];
}

export interface VisualBundle {
  tenant?: string;
  description?: string;
  timezone?: string;
  labels: LabelDecl[];
  groups: string[];
  scopes: string[];
  rules: RuleView[];
  /** Whether rules can be added, removed and moved: `rules` is a block list. */
  structural: boolean;
  /** Top-level keys the view does not model. */
  unmodelled: Unmodelled[];
}

/** Why a parsed source cannot be shown visually. */
export type NotVisual = "not_yaml" | "not_mapping";

const TOP_MODELLED = new Set(["schema_version", "tenant", "description", "timezone", "labels", "groups", "scopes", "rules"]);
const RULE_MODELLED = new Set(["id", "description", "effect", "reason", "match", "route", "obligations", "cache"]);
const MODELLED_TERMS: Readonly<Record<string, ReadonlySet<string>>> = {
  subject: new Set(["ids", "sources", "groups", "auth_methods", "mfa"]),
  device: new Set([]),
  context: new Set(["days", "source_cidrs", "proxy_ids"]),
  target: new Set(["hostnames", "labels", "zones"]),
  grant: new Set(["required", "scopes", "origins"]),
};
const ROUTE_MODELLED = new Set([
  "intent",
  "target",
  "channels",
  "requests",
  "filter",
  "credentials",
  "enforcement",
  "algorithm_profile",
  "algorithm_floor",
  "require_session_capture",
]);

export function visualOf(d: PolicyDocument): VisualBundle | NotVisual {
  if (!d.ok) return d.doc.errors.length ? "not_yaml" : "not_mapping";
  const src = d.source;
  const top = d.doc.contents as YAMLMap | null;
  const out: VisualBundle = { labels: [], groups: [], scopes: [], rules: [], structural: false, unmodelled: [] };
  if (!top) return { ...out, structural: false };
  const line = (n: unknown) => lineOf(src, startOf(n) ?? 0);
  for (const p of top.items) {
    const k = keyOf(p);
    if (k === undefined || !isPair(p)) continue;
    const v = p.value;
    if (!TOP_MODELLED.has(k)) {
      out.unmodelled.push({ path: k, line: line(p.key) });
      continue;
    }
    if (k === "tenant") out.tenant = str(v);
    if (k === "description") out.description = str(v);
    if (k === "timezone") out.timezone = str(v);
    if (k === "groups") out.groups = strings(v);
    if (k === "labels" && isMap(v)) {
      for (const lp of v.items) {
        const lk = keyOf(lp);
        if (lk !== undefined && isPair(lp)) out.labels.push({ key: lk, values: strings(lp.value) });
      }
    }
    if (k === "scopes" && isMap(v)) {
      for (const sp of v.items) {
        const sk = keyOf(sp);
        if (sk !== undefined) out.scopes.push(sk);
      }
    }
    if (k === "rules" && isSeq(v)) {
      out.structural = !v.flow;
      v.items.forEach((item, index) => out.rules.push(ruleOf(src, item, index)));
    }
  }
  return out;
}

function ruleOf(src: string, item: unknown, index: number): RuleView {
  const start = startOf(item) ?? 0;
  const end = (item as { range?: [number, number, number] }).range?.[1] ?? start;
  const rule: RuleView = {
    index,
    id: "",
    line: lineOf(src, start),
    endLine: lineOf(src, Math.max(start, end - 1)),
    match: [],
    routePresent: false,
    obligations: [],
    unmodelled: [],
  };
  if (!isMap(item)) {
    rule.unmodelled.push({ path: "", line: rule.line });
    return rule;
  }
  const line = (n: unknown) => lineOf(src, startOf(n) ?? start);
  for (const p of item.items) {
    const k = keyOf(p);
    if (k === undefined || !isPair(p)) continue;
    const v = p.value;
    if (!RULE_MODELLED.has(k)) {
      rule.unmodelled.push({ path: k, line: line(p.key) });
      continue;
    }
    if (k === "id") rule.id = str(v) ?? "";
    if (k === "description") rule.description = str(v);
    if (k === "effect") rule.effect = str(v);
    if (k === "reason") rule.reason = str(v);
    if (k === "match") {
      if (!isMap(v)) {
        if (v !== null && !(isScalar(v) && v.value === null)) rule.unmodelled.push({ path: "match", line: line(p.key) });
        continue;
      }
      for (const ap of v.items) {
        const axis = keyOf(ap);
        if (axis === undefined || !isPair(ap)) continue;
        const known = MODELLED_TERMS[axis];
        if (!known || !isMap(ap.value)) {
          rule.unmodelled.push({ path: `match.${axis}`, line: line(ap.key) });
          continue;
        }
        for (const tp of ap.value.items) {
          const term = keyOf(tp);
          if (term === undefined || !isPair(tp)) continue;
          const tv = tp.value;
          if (term === "labels" || term === "claims" || term === "posture") {
            if (isMap(tv)) {
              for (const lp of tv.items) {
                const lk = keyOf(lp);
                if (lk !== undefined && isPair(lp)) {
                  rule.match.push({ axis, term: `${term}.${lk}`, values: strings(lp.value), modelled: known.has(term) });
                }
              }
            } else rule.unmodelled.push({ path: `match.${axis}.${term}`, line: line(tp.key) });
            if (!known.has(term)) rule.unmodelled.push({ path: `match.${axis}.${term}`, line: line(tp.key) });
            continue;
          }
          const values = isSeq(tv) || isScalar(tv) ? strings(tv) : [];
          rule.match.push({ axis, term, values, modelled: known.has(term) && (isSeq(tv) || isScalar(tv)) });
          if (!known.has(term) || !(isSeq(tv) || isScalar(tv))) rule.unmodelled.push({ path: `match.${axis}.${term}`, line: line(tp.key) });
        }
      }
    }
    if (k === "route") {
      rule.routePresent = true;
      if (isMap(v)) rule.route = routeOf(v, rule, line);
      else if (!(isScalar(v) && v.value === null)) rule.unmodelled.push({ path: "route", line: line(p.key) });
    }
    if (k === "obligations") {
      if (isSeq(v)) {
        for (const o of v.items) {
          const kind = isMap(o) ? str(o.get("kind", true)) : undefined;
          if (kind !== undefined) rule.obligations.push(kind);
          if (isMap(o)) {
            for (const op of o.items) {
              const ok = keyOf(op);
              if (ok !== undefined && ok !== "kind") rule.unmodelled.push({ path: `obligations.${kind ?? ""}.${ok}`, line: line(isPair(op) ? op.key : o) });
            }
          }
        }
      } else rule.unmodelled.push({ path: "obligations", line: line(p.key) });
    }
    if (k === "cache") {
      if (isMap(v)) {
        const ttl = Number(str(v.get("ttl_seconds", true)));
        rule.cache = { key: strings(v.get("key", true)), ttl: Number.isFinite(ttl) ? ttl : undefined };
      } else rule.unmodelled.push({ path: "cache", line: line(p.key) });
    }
  }
  return rule;
}

function routeOf(v: YAMLMap, rule: RuleView, line: (n: unknown) => number): RouteView {
  const route: RouteView = { channels: [], requestTypes: [], credentials: [] };
  for (const p of v.items) {
    const k = keyOf(p);
    if (k === undefined || !isPair(p)) continue;
    const x = p.value;
    if (!ROUTE_MODELLED.has(k)) {
      rule.unmodelled.push({ path: `route.${k}`, line: line(p.key) });
      continue;
    }
    if (k === "intent") route.intent = str(x);
    if (k === "target") route.target = str(x);
    if (k === "channels") route.channels = strings(x);
    if (k === "algorithm_profile") route.algorithmProfile = str(x);
    if (k === "algorithm_floor") route.algorithmFloor = str(x);
    if (k === "require_session_capture") route.requireSessionCapture = isScalar(x) ? x.value === true : undefined;
    const nested = (name: string, keep: readonly string[], read: (m: YAMLMap) => void) => {
      if (!isMap(x)) {
        rule.unmodelled.push({ path: `route.${name}`, line: line(p.key) });
        return;
      }
      for (const np of x.items) {
        const nk = keyOf(np);
        if (nk !== undefined && !keep.includes(nk) && isPair(np)) rule.unmodelled.push({ path: `route.${name}.${nk}`, line: line(np.key) });
      }
      read(x);
    };
    if (k === "requests") nested("requests", ["types"], (m) => (route.requestTypes = strings(m.get("types", true))));
    if (k === "filter")
      nested("filter", ["mode", "exec_mode"], (m) => {
        route.filterMode = str(m.get("mode", true));
        route.execMode = str(m.get("exec_mode", true));
      });
    if (k === "enforcement")
      nested("enforcement", ["execution", "reach"], (m) => {
        route.execution = str(m.get("execution", true));
        route.reach = str(m.get("reach", true));
      });
    if (k === "credentials") {
      if (!isSeq(x)) {
        rule.unmodelled.push({ path: "route.credentials", line: line(p.key) });
        continue;
      }
      x.items.forEach((e, i) => {
        if (!isMap(e)) {
          route.credentials.push({ other: [] });
          return;
        }
        const u = e.get("username", true);
        const c: Credential = {
          method: str(e.get("method", true)),
          usernameSource: isMap(u) ? str(u.get("from", true)) : str(u) !== undefined ? "literal" : undefined,
          usernameLiteral: isMap(u) ? undefined : str(u),
          other: [],
        };
        for (const ep of e.items) {
          const ek = keyOf(ep);
          if (ek !== undefined && ek !== "method" && ek !== "username") c.other.push(ek);
        }
        if (c.other.length) rule.unmodelled.push({ path: `route.credentials.${i}`, line: line(e) });
        route.credentials.push(c);
      });
    }
  }
  return route;
}

/** The note a line is about: the rule whose lines hold it, or none (the document). */
export function ruleAtLine(rules: readonly RuleView[], line: number | undefined): RuleView | undefined {
  if (!line) return undefined;
  return rules.find((r) => line >= r.line && line <= r.endLine);
}

// ---------------------------------------------------------------------------
// Writing: an edit is the smallest change to the text that says it.
// ---------------------------------------------------------------------------

export type Edit =
  /** Sets the value at a path; `undefined` removes the key (and any map it empties, up to the rule). */
  | { op: "set"; path: Path; value: Value | undefined }
  /** Sets a rule's obligations to these kinds: kept ones keep what they carry, removed ones go, new ones are appended. */
  | { op: "obligations"; rule: number; kinds: readonly string[] }
  /** Sets one ladder entry's username: a literal, or derived (`{from: …}`). */
  | { op: "username"; rule: number; entry: number; source: string; literal?: string }
  /** Appends a ladder entry naming a method. */
  | { op: "addCredential"; rule: number; method: string }
  | { op: "removeCredential"; rule: number; entry: number }
  /** Moves a rule to another place in the order rules are tried. */
  | { op: "move"; from: number; to: number }
  /** Appends a rule: an id and a deny with a reason left to the author. */
  | { op: "addRule"; id: string }
  /** Removes a rule, with the comments written above and inside it. */
  | { op: "removeRule"; index: number };

export function applyEdit(d: PolicyDocument, e: Edit): Applied {
  // A comment the author wrote survives every edit but a rule's removal,
  // which takes the comments written with that rule (and says so).
  return attempt(d, () => edit(d, e), e.op === "removeRule" ? ruleComments(d, e.index) : []);
}

/** Applies edits in order, each to the source the last one wrote. */
export function applyEdits(source: string, edits: readonly Edit[]): Applied {
  let s = source;
  for (const e of edits) {
    const a = applyEdit(parsePolicy(s), e);
    if (a.kind === "refused") return a;
    s = a.source;
  }
  return { kind: "ok", source: s };
}

function edit(d: PolicyDocument, e: Edit): string {
  switch (e.op) {
    case "set":
      return setAt(d, e.path, e.value);
    case "obligations":
      return setObligations(d, e.rule, e.kinds);
    case "username":
      return setUsername(d, e.rule, e.entry, e.source, e.literal);
    case "addCredential":
      return appendItem(d, ["rules", e.rule, "route", "credentials"], { method: e.method });
    case "removeCredential":
      return removeItem(d, ["rules", e.rule, "route", "credentials"], e.entry);
    case "move":
      return moveRule(d, e.from, e.to);
    case "addRule":
      return addRule(d, e.id);
    case "removeRule":
      return removeRule(d, e.index);
  }
}

function setObligations(d: PolicyDocument, rule: number, kinds: readonly string[]): string {
  const path: Path = ["rules", rule, "obligations"];
  const seq = nodeAt(d, path);
  let s = d.source;
  if (isSeq(seq)) {
    const have = seq.items.map((o) => (isMap(o) ? str(o.get("kind", true)) : undefined));
    for (let i = have.length - 1; i >= 0; i--) {
      const k = have[i];
      if (k === undefined || !kinds.includes(k)) s = removeItem(parsePolicy(s), path, i);
    }
    for (const k of kinds) if (!have.includes(k)) s = appendItem(parsePolicy(s), path, { kind: k });
    return s;
  }
  if (seq !== undefined && seq !== null && !(isScalar(seq) && seq.value === null)) throw new Refused("structure");
  for (const k of kinds) s = appendItem(parsePolicy(s), path, { kind: k });
  return s;
}

function setUsername(d: PolicyDocument, rule: number, entry: number, source: string, literal?: string): string {
  const path: Path = ["rules", rule, "route", "credentials", entry, "username"];
  if (source === "literal") return setAt(d, path, literal ?? "");
  const node = nodeAt(d, path);
  if (isMap(node)) return setAt(d, [...path, "from"], source);
  return setAt(d, path, { raw: `{from: ${flowScalar(source)}}` });
}

// --- rules ------------------------------------------------------------------

const RULES: Path = ["rules"];

function moveRule(d: PolicyDocument, from: number, to: number): string {
  return moveItem(d, RULES, from, to);
}

function addRule(d: PolicyDocument, id: string): string {
  const src = d.source;
  const seq = nodeAt(d, RULES);
  const text = (indent: string) => `${indent}- id: ${blockScalar(id)}\n${indent}  effect: deny\n${indent}  reason: ""\n`;
  if (seq === undefined || seq === null || (isScalar(seq) && seq.value === null)) {
    const top = d.doc.contents;
    if (top !== null && !isMap(top)) throw new Refused("structure");
    const pair = isMap(top) ? (top as YAMLMap).items.find((p) => keyOf(p) === "rules") : undefined;
    if (pair) {
      const colon = src.indexOf(":", range(pair.key)[1]);
      const eol = lineEnd(src, colon);
      return splice(src, eol, eol, text("  "));
    }
    return withNewline(src) + `rules:\n${text("  ")}`;
  }
  if (!isSeq(seq) || seq.flow) throw new Refused("structure");
  if (seq.items.length === 0) throw new Refused("structure");
  const l = layoutOf(d, RULES);
  const lastSpan = l.spans[l.spans.length - 1] as string;
  const indent = " ".repeat(column(src, itemSpan(src, blockSeq(d, RULES), seq.items.length - 1, false).dash));
  const gap = l.gaps[l.gaps.length - 1] ?? "";
  return joinLayout({ ...l, spans: [...l.spans.slice(0, -1), withNewline(lastSpan), text(indent)], gaps: [...l.gaps, gap] });
}

function removeRule(d: PolicyDocument, index: number): string {
  // The only rule leaves `rules: []`, which the compiler refuses
  // (policy.no_rules), and the page says so on the document.
  return removeSpan(d, RULES, index);
}

/** The comments written with a rule — above it and inside it — which its removal takes. */
export function ruleComments(d: PolicyDocument, index: number): string[] {
  return spanComments(d, RULES, index);
}

/** A rule id the bundle does not use yet: `rule-<n>`, counting up from the rules there are. */
export function freshRuleId(rules: readonly { id: string }[]): string {
  const taken = new Set(rules.map((r) => r.id));
  for (let n = rules.length + 1; ; n++) {
    const id = `rule-${n}`;
    if (!taken.has(id)) return id;
  }
}


// ---------------------------------------------------------------------------
// Free text, checked where the server checks it, with the server's codes.
// ---------------------------------------------------------------------------

/** A check's refusal: a compiler code and its parameters, rendered by policyCodes.ts like the server's own. */
export interface FieldRefusal {
  code: string;
  parameters?: Record<string, string>;
}

/** A rule's id: present, and used once (model.Bundle.Validate's `rule.id_missing`, `rule.id_duplicate`). */
export function checkRuleId(id: string, rules: readonly RuleView[], index: number): FieldRefusal | null {
  if (id.trim() === "") return { code: "rule.id_missing" };
  const other = rules.find((r) => r.index !== index && r.id === id);
  return other ? { code: "rule.id_duplicate", parameters: { id, other_line: String(other.line) } } : null;
}

/** A deny's reason: what an operator reads, so never blank (`rule.deny_needs_reason`). */
export function checkReason(reason: string): FieldRefusal | null {
  return reason.trim() === "" ? { code: "rule.deny_needs_reason" } : null;
}

/** A hostname or `*.suffix` pattern (model.ValidHostPattern, `match.invalid_hostname_pattern`). */
export function checkHostPattern(p: string): FieldRefusal | null {
  const ok = p !== "" && p !== "*" && (p.startsWith("*.") ? p.length > 2 && !p.slice(2).includes("*") : !p.includes("*"));
  return ok ? null : { code: "match.invalid_hostname_pattern", parameters: { pattern: p } };
}

/** A network: a prefix, or one address (compile.parseNetwork over Go's netip, `match.invalid_cidr`). */
export function checkCidr(s: string): FieldRefusal | null {
  return isNetwork(s) ? null : { code: "match.invalid_cidr", parameters: { cidr: s } };
}

function isIPv4(s: string): boolean {
  const parts = s.split(".");
  return parts.length === 4 && parts.every((p) => /^(0|[1-9][0-9]{0,2})$/.test(p) && Number(p) <= 255);
}

function isIPv6(s: string): boolean {
  let body = s;
  let groups = 8;
  const lastColon = body.lastIndexOf(":");
  if (lastColon >= 0 && body.slice(lastColon + 1).includes(".")) {
    if (!isIPv4(body.slice(lastColon + 1))) return false;
    body = body.slice(0, lastColon + 1) + "0:0";
  }
  const halves = body.split("::");
  if (halves.length > 2) return false;
  const group = (g: string) => /^[0-9a-fA-F]{1,4}$/.test(g);
  const side = (h: string) => (h === "" ? [] : h.split(":"));
  if (halves.length === 1) {
    const gs = side(body);
    return gs.length === groups && gs.every(group);
  }
  const [a, b] = [side(halves[0] ?? ""), side(halves[1] ?? "")];
  groups -= 1; // `::` stands for at least one group
  return a.length + b.length <= groups && [...a, ...b].every(group);
}

function isNetwork(s: string): boolean {
  // netip.ParsePrefix first: an address, `/`, and bits within its length.
  const slash = s.indexOf("/");
  if (slash >= 0) {
    const addr = s.slice(0, slash);
    const bits = s.slice(slash + 1);
    if (/^(0|[1-9][0-9]*)$/.test(bits) && !addr.includes("%")) {
      if (isIPv4(addr) && Number(bits) <= 32) return true;
      if (isIPv6(addr) && Number(bits) <= 128) return true;
    }
  }
  // Then netip.ParseAddr: one address, an IPv6 one with any zone after `%`.
  if (isIPv4(s)) return true;
  const zone = s.indexOf("%");
  return zone < 0 ? isIPv6(s) : zone < s.length - 1 && isIPv6(s.slice(0, zone));
}
