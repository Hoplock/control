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

import { Document, isMap, isPair, isScalar, isSeq, parse, parseDocument, Parser, CST, type Node as YamlNode, type Scalar, type YAMLMap, type YAMLSeq } from "yaml";

export type PathKey = string | number;
export type Path = readonly PathKey[];

/** A value an edit writes: a scalar, a list of scalars, or a map of lists (a label match). */
export type Value = string | number | boolean | readonly string[] | Readonly<Record<string, readonly string[]>>;

/** A source, parsed. `ok` is whether the visual view can show it at all. */
export interface PolicyDocument {
  source: string;
  doc: Document.Parsed;
  /** The parser's tokens: the lossless form `serialise` writes. */
  tokens: CST.Token[];
  /** Whether the text parses as YAML whose top level is a mapping. */
  ok: boolean;
}

export function parsePolicy(source: string): PolicyDocument {
  const tokens = [...new Parser().parse(source)];
  const doc = parseDocument(source, { keepSourceTokens: true, uniqueKeys: false, prettyErrors: false });
  const contents = doc.contents;
  const ok = doc.errors.length === 0 && tokens.filter((t) => t.type === "document").length <= 1 && (contents === null || isMap(contents));
  return { source, doc, tokens, ok };
}

/** The document written back: its concrete syntax, every byte as it was read. */
export function serialise(d: PolicyDocument): string {
  return d.tokens.map((t) => CST.stringify(t)).join("");
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
  port?: number;
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
  "port",
  "channels",
  "requests",
  "filter",
  "credentials",
  "enforcement",
  "algorithm_profile",
  "algorithm_floor",
  "require_session_capture",
]);

function lineOf(src: string, offset: number): number {
  let n = 1;
  for (let i = 0; i < offset && i < src.length; i++) if (src.charCodeAt(i) === 10) n++;
  return n;
}

function startOf(n: unknown): number | undefined {
  const r = (n as { range?: [number, number, number] } | null)?.range;
  return r ? r[0] : undefined;
}

function str(n: unknown): string | undefined {
  if (isScalar(n) && n.value !== null && n.value !== undefined && typeof n.value !== "object") return String(n.value);
  return undefined;
}

function strings(n: unknown): string[] {
  if (isSeq(n)) return n.items.map((i) => str(i)).filter((s): s is string => s !== undefined);
  const s = str(n);
  return s === undefined ? [] : [s];
}

function keyOf(p: unknown): string | undefined {
  return isPair(p) ? str(p.key) : undefined;
}

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
    if (k === "port") {
      const n = Number(str(x));
      route.port = Number.isFinite(n) ? n : undefined;
    }
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

export type Applied = { kind: "ok"; source: string } | { kind: "refused"; reason: EditRefusal };

/**
 * Why an edit was not made: the text holds the value in a form the editor
 * cannot change without rewriting it (`structure`), or the change would lose
 * a comment (`comment_lost`). Either way the author is sent to the source.
 */
export type EditRefusal = "structure" | "comment_lost";

class Refused extends Error {
  constructor(readonly reason: EditRefusal) {
    super(reason);
  }
}

export function applyEdit(d: PolicyDocument, e: Edit): Applied {
  try {
    const next = edit(d, e);
    if (next === d.source) return { kind: "ok", source: next };
    // A comment the author wrote survives every edit but a rule's removal,
    // which takes the comments written with that rule (and says so).
    const allowed = e.op === "removeRule" ? ruleComments(d, e.index) : [];
    if (!keepsComments(d.source, next, allowed)) return { kind: "refused", reason: "comment_lost" };
    return { kind: "ok", source: next };
  } catch (err) {
    if (err instanceof Refused) return { kind: "refused", reason: err.reason };
    throw err;
  }
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

// --- text helpers -----------------------------------------------------------

function lineStart(src: string, off: number): number {
  return src.lastIndexOf("\n", off - 1) + 1;
}

/** The offset just past the end of the line holding `off` (its newline included). */
function lineEnd(src: string, off: number): number {
  const i = src.indexOf("\n", off);
  return i < 0 ? src.length : i + 1;
}

function column(src: string, off: number): number {
  return off - lineStart(src, off);
}

function splice(src: string, from: number, to: number, text: string): string {
  return src.slice(0, from) + text + src.slice(to);
}

function range(n: unknown): [number, number, number] {
  const r = (n as { range?: [number, number, number] } | null)?.range;
  if (!r) throw new Refused("structure");
  return r;
}

/** A source's comments, each by its text, from the concrete syntax tree. */
export function commentsOf(source: string): string[] {
  const out: string[] = [];
  const walk = (x: unknown) => {
    if (Array.isArray(x)) {
      for (const y of x) walk(y);
      return;
    }
    if (!x || typeof x !== "object") return;
    const t = x as { type?: string; source?: string };
    if (t.type === "comment" && typeof t.source === "string") {
      out.push(t.source.trim());
      return;
    }
    for (const v of Object.values(x)) if (v && typeof v === "object") walk(v);
  };
  for (const t of new Parser().parse(source)) walk(t);
  return out;
}

function keepsComments(before: string, after: string, allowed: readonly string[]): boolean {
  const left = new Map<string, number>();
  for (const c of commentsOf(after)) left.set(c, (left.get(c) ?? 0) + 1);
  const spare = new Map<string, number>();
  for (const c of allowed) spare.set(c, (spare.get(c) ?? 0) + 1);
  for (const c of commentsOf(before)) {
    const n = left.get(c) ?? 0;
    if (n > 0) left.set(c, n - 1);
    else if ((spare.get(c) ?? 0) > 0) spare.set(c, (spare.get(c) ?? 0) - 1);
    else return false;
  }
  return true;
}

// A scalar the editor writes: plain where YAML reads it back as the same
// value, quoted otherwise. A value that would read back as another type —
// `true`, `0755`, `null` — is quoted, so a string stays a string.
const FLOW_PLAIN = /^[A-Za-z0-9_][A-Za-z0-9_./@+-]*$/;

function readsAs(text: string, v: string | number | boolean): boolean {
  try {
    return parse(text) === v;
  } catch {
    return false;
  }
}

function blockScalar(v: string | number | boolean, style?: Scalar.Type): string {
  if (typeof v !== "string") return String(v);
  if (style === "QUOTE_SINGLE") return `'${v.replaceAll("'", "''")}'`;
  if (style === "QUOTE_DOUBLE") return JSON.stringify(v);
  const plain = new Document(v).toString({ lineWidth: 0 }).replace(/\n$/, "");
  return !plain.includes("\n") && readsAs(plain, v) ? plain : JSON.stringify(v);
}

function flowScalar(v: string | number | boolean): string {
  if (typeof v !== "string") return String(v);
  return FLOW_PLAIN.test(v) && readsAs(v, v) ? v : JSON.stringify(v);
}

function flowList(values: readonly string[]): string {
  return `[${values.map((v) => flowScalar(v)).join(", ")}]`;
}

function flowMapOfLists(m: Readonly<Record<string, readonly string[]>>): string {
  return `{${Object.entries(m)
    .map(([k, vs]) => `${flowScalar(k)}: ${flowList(vs)}`)
    .join(", ")}}`;
}

/** Text written as it is: what the editor composes itself (`{from: subject}`). */
interface Raw {
  raw: string;
}
type Inner = Value | Raw;

function isRaw(v: Inner): v is Raw {
  return typeof v === "object" && !Array.isArray(v) && "raw" in v && typeof (v as Raw).raw === "string";
}

function isList(v: Inner): v is readonly string[] {
  return Array.isArray(v);
}

function isMapValue(v: Inner): v is Readonly<Record<string, readonly string[]>> {
  return typeof v === "object" && !Array.isArray(v) && !isRaw(v);
}

/** The value as a flow or plain text, for the place a key's value goes. */
function valueText(v: Inner): string {
  if (isRaw(v)) return v.raw;
  if (isList(v)) return flowList(v);
  if (isMapValue(v)) return flowMapOfLists(v);
  return blockScalar(v);
}

/** New keys, nested from a path's missing tail, in block style at an indent. */
function blockPairs(keys: readonly string[], v: Inner, indent: string): string {
  const [head, ...rest] = keys;
  if (head === undefined) throw new Refused("structure");
  if (rest.length === 0) return `${indent}${flowScalar(head)}: ${valueText(v)}\n`;
  return `${indent}${flowScalar(head)}:\n${blockPairs(rest, v, indent + "  ")}`;
}

function nodeAt(d: PolicyDocument, path: Path): unknown {
  return d.doc.getIn(path, true);
}

function hasComment(src: string, from: number, to: number): boolean {
  return commentsOf(src.slice(from, to)).length > 0;
}

// --- set --------------------------------------------------------------------

function setAt(d: PolicyDocument, path: Path, value: Inner | undefined): string {
  const src = d.source;
  if (value === undefined || (isList(value) && value.length === 0) || (isMapValue(value) && Object.keys(value).length === 0)) {
    return d.doc.hasIn(path) ? removeKey(d, path) : src;
  }
  const node = nodeAt(d, path);
  if (node !== undefined && node !== null) return replaceValue(d, path, node, value);
  // Missing: nested under the deepest map on the path that exists.
  let k = path.length - 1;
  while (k > 0 && !isMap(nodeAt(d, path.slice(0, k)))) k--;
  const parent = k === 0 ? d.doc.contents : nodeAt(d, path.slice(0, k));
  const keys = path.slice(k).map((x) => {
    if (typeof x !== "string") throw new Refused("structure");
    return x;
  });
  // A key on the way that is there and holds nothing (`match:`) takes the rest beneath it.
  const pair = isMap(parent) ? parent.items.find((p) => keyOf(p) === keys[0]) : undefined;
  if (pair && isPair(pair)) return insertUnderEmptyKey(src, pair, keys.slice(1), value);
  return insertKey(src, parent, keys, value);
}

function replaceValue(d: PolicyDocument, path: Path, node: unknown, value: Inner): string {
  const src = d.source;
  const [start, end] = range(node);
  if (isScalar(node)) {
    if (node.value === null || isList(value) || isMapValue(value) || isRaw(value)) {
      if (node.value !== null && !isRaw(value)) throw new Refused("structure");
      if (isRaw(value) && node.value !== null) return splice(src, start, end, value.raw);
      const parentPath = path.slice(0, -1);
      const parent = parentPath.length === 0 ? d.doc.contents : nodeAt(d, parentPath);
      const pair = isMap(parent) ? parent.items.find((p) => keyOf(p) === path[path.length - 1]) : undefined;
      if (!pair || !isPair(pair)) throw new Refused("structure");
      return insertUnderEmptyKey(src, pair, [], value);
    }
    const flow = inFlow(d, path);
    return splice(src, start, end, flow ? flowScalar(value) : blockScalar(value, node.type));
  }
  if (isSeq(node) && isList(value)) {
    if (node.flow) {
      if (hasComment(src, start, end)) throw new Refused("structure");
      return splice(src, start, end, flowList(value));
    }
    return setBlockList(d, path, node, value);
  }
  if ((isMap(node) || isSeq(node)) && node.flow && !isList(value) && !isMapValue(value)) {
    // A flow collection that becomes a scalar (`{from: subject}` → `svc`), where it stands.
    if (hasComment(src, start, end)) throw new Refused("structure");
    return splice(src, start, end, isRaw(value) ? value.raw : inFlow(d, path) ? flowScalar(value) : blockScalar(value));
  }
  if (isMap(node) && isMapValue(value)) {
    if (node.flow) {
      if (hasComment(src, start, end)) throw new Refused("structure");
      return splice(src, start, end, flowMapOfLists(value));
    }
    // A block map of lists: key by key, each a smaller edit.
    let s = src;
    const existing = node.items.map((p) => keyOf(p)).filter((x): x is string => x !== undefined);
    for (const k of existing) if (!(k in value)) s = setAt(parsePolicy(s), [...path, k], undefined);
    for (const [k, vs] of Object.entries(value)) s = setAt(parsePolicy(s), [...path, k], vs);
    return s;
  }
  throw new Refused("structure");
}

function inFlow(d: PolicyDocument, path: Path): boolean {
  for (let i = path.length - 1; i > 0; i--) {
    const n = nodeAt(d, path.slice(0, i));
    if ((isMap(n) || isSeq(n)) && n.flow) return true;
  }
  return false;
}

/** A block list set item by item: removed items go with their own lines, new ones are appended. */
function setBlockList(d: PolicyDocument, path: Path, node: YAMLSeq, value: readonly string[]): string {
  let s = d.source;
  const have = node.items.map((i) => str(i));
  // Remove from the end, so earlier items keep their place.
  for (let i = have.length - 1; i >= 0; i--) {
    const v = have[i];
    if (v === undefined || !value.includes(v)) s = removeItem(parsePolicy(s), path, i);
  }
  const kept = new Set(have.filter((v): v is string => v !== undefined && value.includes(v)));
  for (const v of value) if (!kept.has(v)) s = appendItem(parsePolicy(s), path, v);
  return s;
}

function insertKey(src: string, parent: unknown, keys: readonly string[], value: Inner): string {
  if (!isMap(parent)) throw new Refused("structure");
  if (parent.flow) {
    const [start, end] = range(parent);
    if (hasComment(src, start, end)) throw new Refused("structure");
    if (parent.items.length === 0) {
      // `{}`: becomes a block map under its key.
      const indent = " ".repeat(column(src, lineStartKey(src, start)) + 2);
      const before = src.slice(0, start).replace(/[ \t]+$/, "");
      return before + "\n" + blockPairs(keys, value, indent).replace(/\n$/, "") + src.slice(end);
    }
    const body = src.slice(start + 1, end - 1).trim();
    return splice(src, start, end, `{${body}, ${flowPair(keys, value)}}`);
  }
  const last = parent.items[parent.items.length - 1];
  if (!last || !isPair(last)) throw new Refused("structure");
  const first = parent.items[0];
  const indent = " ".repeat(column(src, range(isPair(first) ? first.key : first)[0]));
  const lastEnd = range(last.value ?? last.key)[1];
  const at = lineEnd(src, Math.max(lastEnd - 1, range(last.key)[0]));
  const text = blockPairs(keys, value, indent);
  return at === src.length && !src.endsWith("\n") ? splice(src, at, at, "\n" + text.replace(/\n$/, "")) : splice(src, at, at, text);
}

/** The offset of the key whose value starts at `valueStart` (the start of its line's content). */
function lineStartKey(src: string, valueStart: number): number {
  const ls = lineStart(src, valueStart);
  let i = ls;
  while (src[i] === " ") i++;
  if (src[i] === "-") {
    i++;
    while (src[i] === " ") i++;
  }
  return i;
}

function flowPair(keys: readonly string[], v: Inner): string {
  const [head, ...rest] = keys;
  if (head === undefined) throw new Refused("structure");
  if (rest.length > 0) return `${flowScalar(head)}: {${flowPair(rest, v)}}`;
  const text = isRaw(v) ? v.raw : isList(v) ? flowList(v) : isMapValue(v) ? flowMapOfLists(v) : flowScalar(v);
  return `${flowScalar(head)}: ${text}`;
}

/**
 * `key:` holding nothing (or `~`, `null`): the value is written after the
 * colon, or the nested keys on the lines beneath it. A comment on the line
 * stays where it was.
 */
function insertUnderEmptyKey(src: string, pair: { key: unknown; value: unknown }, keys: readonly string[], value: Inner): string {
  const [ks, ke] = range(pair.key);
  const colon = src.indexOf(":", ke);
  if (colon < 0) throw new Refused("structure");
  const eol = lineEnd(src, colon);
  const m = /^[ \t]*(?:~|null|Null|NULL)?([ \t]*(?:#.*)?)(\n?)$/.exec(src.slice(colon + 1, eol));
  if (!m) throw new Refused("structure");
  const comment = (m[1] ?? "").replace(/^[ \t]+$/, "");
  const newline = m[2] ?? "";
  if (keys.length === 0) return src.slice(0, colon + 1) + ` ${valueText(value)}` + comment + newline + src.slice(eol);
  const text = blockPairs(keys, value, " ".repeat(column(src, ks) + 2));
  return src.slice(0, colon + 1) + comment + "\n" + text + src.slice(eol);
}

/** Removes a key; a map it leaves empty goes too, up to (never including) the rule. */
function removeKey(d: PolicyDocument, path: Path): string {
  const src = d.source;
  const parentPath = path.slice(0, -1);
  const parent = parentPath.length === 0 ? d.doc.contents : nodeAt(d, parentPath);
  const key = path[path.length - 1];
  if (!isMap(parent)) {
    if (isSeq(parent) && typeof key === "number") return removeItem(d, parentPath, key);
    throw new Refused("structure");
  }
  const idx = parent.items.findIndex((p) => keyOf(p) === key);
  const pair = parent.items[idx];
  if (!pair || !isPair(pair)) return src;
  // A rule is never emptied by a field's removal: its path is ["rules", n].
  const isRuleField = parentPath.length === 2 && parentPath[0] === "rules";
  if (parent.items.length === 1 && parentPath.length > 0 && !isRuleField) {
    return removeKey(d, parentPath);
  }
  if (parent.flow) {
    const [start, end] = range(parent);
    if (hasComment(src, start, end)) throw new Refused("structure");
    const rest = parent.items.filter((_, i) => i !== idx);
    const body = rest.map((p) => (isPair(p) ? src.slice(range(p.key)[0], range(p.value ?? p.key)[1]) : "")).join(", ");
    return splice(src, start, end, `{${body}}`);
  }
  const [ks] = range(pair.key);
  const ve = range(pair.value ?? pair.key)[1];
  const from = lineStart(src, ks);
  // A key on its item's `- ` line is the item's first: the item goes, not the key.
  if (src.slice(from, ks).trim() !== "") throw new Refused("structure");
  const to = lineEnd(src, Math.max(ve - 1, ks));
  return splice(src, from, to, keptComments(src, from, to));
}

/** The comments inside a removed stretch, each kept as a line of its own at its indent. */
function keptComments(src: string, from: number, to: number): string {
  const lines: string[] = [];
  const text = src.slice(from, to);
  if (commentsOf(text).length === 0) return "";
  const indent = " ".repeat(column(src, from + (text.length - text.trimStart().length)));
  for (const c of commentsOf(text)) lines.push(`${indent}${c}\n`);
  return lines.join("");
}

// --- sequences --------------------------------------------------------------

/** The text of a block list's item: from its `-` line (its comments above it with it) to the end of its last line. */
function itemSpan(src: string, seq: YAMLSeq, i: number, withHeader: boolean): { start: number; end: number; dash: number } {
  const tok = (seq as { srcToken?: CST.BlockSequence }).srcToken;
  const it = tok?.type === "block-seq" ? tok.items[i] : undefined;
  const ind = it?.start.find((s) => s.type === "seq-item-ind");
  if (!it || !ind) throw new Refused("structure");
  const item = seq.items[i];
  const dash = ind.offset;
  let start = lineStart(src, dash);
  if (withHeader) {
    const col = column(src, dash);
    for (;;) {
      if (start === 0) break;
      const prev = lineStart(src, start - 1);
      const line = src.slice(prev, start);
      const content = line.trimStart();
      if (!content.startsWith("#") || line.length - content.length > col) break;
      start = prev;
    }
  }
  const valueEnd = item ? range(item)[1] : dash + 1;
  const end = lineEnd(src, Math.max(valueEnd - 1, dash));
  return { start, end, dash };
}

function removeItem(d: PolicyDocument, path: Path, i: number): string {
  const src = d.source;
  const seq = nodeAt(d, path);
  if (!isSeq(seq)) throw new Refused("structure");
  if (seq.items.length === 1 && path.length > 0) return removeKey(d, path);
  if (seq.flow) {
    const [start, end] = range(seq);
    if (hasComment(src, start, end)) throw new Refused("structure");
    const rest = seq.items.filter((_, j) => j !== i).map((n) => src.slice(range(n)[0], range(n)[1]));
    return splice(src, start, end, `[${rest.join(", ")}]`);
  }
  const { start, end } = itemSpan(src, seq, i, false);
  return splice(src, start, end, keptComments(src, start, end));
}

function appendItem(d: PolicyDocument, path: Path, v: string | Readonly<Record<string, string>>): string {
  const src = d.source;
  const seq = nodeAt(d, path);
  if (seq === undefined || seq === null || (isScalar(seq) && seq.value === null)) {
    // No list yet: it is created holding this one item.
    if (typeof v === "string") return setAt(d, path, [v]);
    return createMapList(d, path, v);
  }
  if (!isSeq(seq)) throw new Refused("structure");
  if (seq.flow) {
    const [start, end] = range(seq);
    if (hasComment(src, start, end)) throw new Refused("structure");
    if (typeof v !== "string") throw new Refused("structure");
    const items = seq.items.map((n) => src.slice(range(n)[0], range(n)[1]));
    return splice(src, start, end, `[${[...items, flowScalar(v)].join(", ")}]`);
  }
  const last = itemSpan(src, seq, seq.items.length - 1, false);
  const indent = " ".repeat(column(src, last.dash));
  const text = typeof v === "string" ? `${indent}- ${blockScalar(v)}\n` : mapItem(v, indent);
  return last.end === src.length && !src.endsWith("\n") ? splice(src, last.end, last.end, "\n" + text.replace(/\n$/, "")) : splice(src, last.end, last.end, text);
}

function mapItem(v: Readonly<Record<string, string>>, indent: string): string {
  return Object.entries(v)
    .map(([k, x], i) => `${i === 0 ? `${indent}- ` : `${indent}  `}${flowScalar(k)}: ${blockScalar(x)}\n`)
    .join("");
}

/** A list of maps created under a key that has none (`obligations:`, `credentials:`). */
function createMapList(d: PolicyDocument, path: Path, v: Readonly<Record<string, string>>): string {
  const src = d.source;
  const parentPath = path.slice(0, -1);
  const key = path[path.length - 1];
  const parent = nodeAt(d, parentPath);
  if (typeof key !== "string") throw new Refused("structure");
  if (!isMap(parent) || parent.flow) {
    // A missing parent: made first as a map holding a placeholder key, which is then the list.
    throw new Refused("structure");
  }
  const pair = parent.items.find((p) => keyOf(p) === key);
  const first = parent.items[0];
  const indent = column(src, range(isPair(first) ? first.key : first)[0]);
  const list = mapItem(v, " ".repeat(indent + 2));
  if (pair && isPair(pair)) {
    const colon = src.indexOf(":", range(pair.key)[1]);
    const eol = lineEnd(src, colon);
    const tail = src.slice(colon + 1, eol).replace(/\s*(#.*)?\n?$/, "");
    if (tail.trim() !== "" && tail.trim() !== "~" && tail.trim() !== "null" && tail.trim() !== "[]") throw new Refused("structure");
    const cleared = splice(src, colon + 1, colon + 1 + tail.length, "");
    const at = eol - tail.length;
    return splice(cleared, at, at, list);
  }
  const last = parent.items[parent.items.length - 1];
  if (!last || !isPair(last)) throw new Refused("structure");
  const at = lineEnd(src, Math.max(range(last.value ?? last.key)[1] - 1, range(last.key)[0]));
  return splice(src, at, at, `${" ".repeat(indent)}${key}:\n${list}`);
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

function rulesSeq(d: PolicyDocument): YAMLSeq {
  const rules = nodeAt(d, ["rules"]);
  if (!isSeq(rules) || rules.flow) throw new Refused("structure");
  return rules;
}

interface Layout {
  head: string;
  spans: string[];
  gaps: string[];
  tail: string;
}

/** The rules list cut into each rule's own lines and the gaps between them. */
function layout(d: PolicyDocument): Layout {
  const src = d.source;
  const seq = rulesSeq(d);
  const spans = seq.items.map((_, i) => itemSpan(src, seq, i, true));
  const first = spans[0];
  const last = spans[spans.length - 1];
  if (!first || !last) throw new Refused("structure");
  const gaps: string[] = [];
  for (let i = 0; i + 1 < spans.length; i++) gaps.push(src.slice((spans[i] as { end: number }).end, (spans[i + 1] as { start: number }).start));
  return { head: src.slice(0, first.start), spans: spans.map((s) => src.slice(s.start, s.end)), gaps, tail: src.slice(last.end) };
}

function join(l: Layout): string {
  let out = l.head;
  l.spans.forEach((s, i) => {
    out += s;
    if (i < l.spans.length - 1) out += l.gaps[i] ?? "";
  });
  return out + l.tail;
}

function withNewline(s: string): string {
  return s.endsWith("\n") ? s : s + "\n";
}

function moveRule(d: PolicyDocument, from: number, to: number): string {
  const l = layout(d);
  if (from === to || from < 0 || to < 0 || from >= l.spans.length || to >= l.spans.length) return d.source;
  // The last rule's lines may end the file without a newline; inside the list they need one.
  const lastHadNewline = (l.spans[l.spans.length - 1] as string).endsWith("\n");
  const spans = l.spans.map(withNewline);
  const [moved] = spans.splice(from, 1);
  spans.splice(to, 0, moved as string);
  if (!lastHadNewline && l.tail === "") spans[spans.length - 1] = (spans[spans.length - 1] as string).replace(/\n$/, "");
  return join({ ...l, spans });
}

function addRule(d: PolicyDocument, id: string): string {
  const src = d.source;
  const seq = nodeAt(d, ["rules"]);
  const text = (indent: string) => `${indent}- id: ${blockScalar(id)}\n${indent}  effect: deny\n${indent}  reason: ""\n`;
  if (seq === undefined || seq === null || (isScalar(seq) && seq.value === null)) {
    const top = d.doc.contents;
    if (top !== null && !isMap(top)) throw new Refused("structure");
    const pair = isMap(top) ? top.items.find((p) => keyOf(p) === "rules") : undefined;
    if (pair && isPair(pair)) {
      const colon = src.indexOf(":", range(pair.key)[1]);
      const eol = lineEnd(src, colon);
      return splice(src, eol, eol, text("  "));
    }
    return withNewline(src) + `rules:\n${text("  ")}`;
  }
  if (!isSeq(seq) || seq.flow) throw new Refused("structure");
  if (seq.items.length === 0) throw new Refused("structure");
  const l = layout(d);
  const lastSpan = l.spans[l.spans.length - 1] as string;
  const indent = " ".repeat(column(src, itemSpan(src, seq, seq.items.length - 1, false).dash));
  const gap = l.gaps[l.gaps.length - 1] ?? "";
  return join({ ...l, spans: [...l.spans.slice(0, -1), withNewline(lastSpan), text(indent)], gaps: [...l.gaps, gap] });
}

function removeRule(d: PolicyDocument, index: number): string {
  const l = layout(d);
  if (index < 0 || index >= l.spans.length) return d.source;
  if (l.spans.length === 1) {
    // The only rule: the list is left empty, which the compiler refuses
    // (policy.no_rules), and the page says so on the document.
    return l.head.replace(/rules:[ \t]*\n$/, "rules: []\n") + l.tail;
  }
  const spans = [...l.spans];
  const gaps = [...l.gaps];
  const wasLast = index === spans.length - 1;
  spans.splice(index, 1);
  gaps.splice(wasLast ? index - 1 : index, 1);
  if (wasLast && !(l.spans[index] as string).endsWith("\n")) spans[spans.length - 1] = (spans[spans.length - 1] as string).replace(/\n$/, "");
  return join({ ...l, spans, gaps });
}

/** The comments written with a rule — above it and inside it — which its removal takes. */
export function ruleComments(d: PolicyDocument, index: number): string[] {
  try {
    const seq = rulesSeq(d);
    const { start, end } = itemSpan(d.source, seq, index, true);
    return commentsOf(d.source.slice(start, end));
  } catch {
    return [];
  }
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
