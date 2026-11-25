// A YAML SOURCE AS A DOCUMENT: THE TEXT-SPLICE CORE (0066, 0068; PLAN §5.6,
// M7). The console's two document editors — the visual policy editor
// (policyDocument.ts) and the visual claim-mapping editor
// (claimMappingDocument.ts) — work on the source an operator wrote, never on
// a copy re-serialised from a model of it: a stored version IS its document
// and that document's digest, and a reviewer reads the diff. So the source
// text is the one truth, and this module is what both build on:
//
//   - `parseYaml` reads it with the `yaml` library's document model, each node
//     carrying its place in the text, and keeps the concrete syntax tree;
//   - `serialise` writes that tree back, every byte as it was read — comments,
//     blank lines, quoting, key order — so a round trip is byte-identical by
//     construction;
//   - the edits change the smallest stretch of text that says them: a value
//     replaced where it stands, a key inserted after its last sibling, a list
//     item removed or moved as its own lines with the comments written above
//     it. Nothing an edit did not name changes, and `attempt` refuses one
//     that would lose a comment (`comment_lost`) or that the text's form
//     cannot take (`structure`).
//
// What each editor reads from the document, and which edits it offers, is its
// own module's; nothing here knows a policy or a mapping. Pure: no React, no
// DOM; every rule is a unit test (test/policyDocument.test.ts,
// test/claimMappingDocument.test.ts).

import { Document, isMap, isPair, isScalar, isSeq, parse, parseDocument, Parser, CST, type Scalar, type YAMLMap, type YAMLSeq } from "yaml";

export type PathKey = string | number;
export type Path = readonly PathKey[];

/** A value an edit writes: a scalar, a list of scalars, or a map of lists (a label match). */
export type Value = string | number | boolean | readonly string[] | Readonly<Record<string, readonly string[]>>;

/** A source, parsed. `ok` is whether the text is one YAML document whose top level is a mapping (or empty). */
export interface YamlDocument {
  source: string;
  doc: Document.Parsed;
  /** The parser's tokens: the lossless form `serialise` writes. */
  tokens: CST.Token[];
  ok: boolean;
  /**
   * A map the document's reader never lets an edit empty away — a policy's
   * rule, which is an item of its list whatever is removed from it. Absent,
   * a map left with no key goes with its last one.
   */
  keep?: (path: Path) => boolean;
}

export function parseYaml(source: string, keep?: (path: Path) => boolean): YamlDocument {
  const tokens = [...new Parser().parse(source)];
  const doc = parseDocument(source, { keepSourceTokens: true, uniqueKeys: false, prettyErrors: false });
  const contents = doc.contents;
  const ok = doc.errors.length === 0 && tokens.filter((t) => t.type === "document").length <= 1 && (contents === null || isMap(contents));
  return keep ? { source, doc, tokens, ok, keep } : { source, doc, tokens, ok };
}

/** Another source parsed as `d` was: an edit made in steps re-reads the text each step wrote. */
export function reparse(d: YamlDocument, source: string): YamlDocument {
  return parseYaml(source, d.keep);
}

/** The document written back: its concrete syntax, every byte as it was read. */
export function serialise(d: YamlDocument): string {
  return d.tokens.map((t) => CST.stringify(t)).join("");
}

// ---------------------------------------------------------------------------
// Reading.
// ---------------------------------------------------------------------------

export function lineOf(src: string, offset: number): number {
  let n = 1;
  for (let i = 0; i < offset && i < src.length; i++) if (src.charCodeAt(i) === 10) n++;
  return n;
}

export function startOf(n: unknown): number | undefined {
  const r = (n as { range?: [number, number, number] } | null)?.range;
  return r ? r[0] : undefined;
}

export function str(n: unknown): string | undefined {
  if (isScalar(n) && n.value !== null && n.value !== undefined && typeof n.value !== "object") return String(n.value);
  return undefined;
}

export function strings(n: unknown): string[] {
  if (isSeq(n)) return n.items.map((i) => str(i)).filter((s): s is string => s !== undefined);
  const s = str(n);
  return s === undefined ? [] : [s];
}

export function keyOf(p: unknown): string | undefined {
  return isPair(p) ? str(p.key) : undefined;
}

export type Applied = { kind: "ok"; source: string } | { kind: "refused"; reason: EditRefusal };

/**
 * Why an edit was not made: the text holds the value in a form the editor
 * cannot change without rewriting it (`structure`), or the change would lose
 * a comment (`comment_lost`). Either way the author is sent to the source.
 */
export type EditRefusal = "structure" | "comment_lost";

export class Refused extends Error {
  constructor(readonly reason: EditRefusal) {
    super(reason);
  }
}

/**
 * Runs an edit on a document and judges what it wrote: a comment the author
 * wrote survives every edit but those listed in `allowed` (the comments
 * written with an item the edit removes, which its announcement counts), and
 * an edit the text's form cannot take is refused rather than made.
 */
export function attempt(d: YamlDocument, edit: () => string, allowed: readonly string[] = []): Applied {
  try {
    const next = edit();
    if (next === d.source) return { kind: "ok", source: next };
    if (!keepsComments(d.source, next, allowed)) return { kind: "refused", reason: "comment_lost" };
    return { kind: "ok", source: next };
  } catch (err) {
    if (err instanceof Refused) return { kind: "refused", reason: err.reason };
    throw err;
  }
}

// ---------------------------------------------------------------------------
// Writing: an edit is the smallest change to the text that says it.
// ---------------------------------------------------------------------------

// --- text helpers -----------------------------------------------------------

export function lineStart(src: string, off: number): number {
  return src.lastIndexOf("\n", off - 1) + 1;
}

/** The offset just past the end of the line holding `off` (its newline included). */
export function lineEnd(src: string, off: number): number {
  const i = src.indexOf("\n", off);
  return i < 0 ? src.length : i + 1;
}

export function column(src: string, off: number): number {
  return off - lineStart(src, off);
}

export function splice(src: string, from: number, to: number, text: string): string {
  return src.slice(0, from) + text + src.slice(to);
}

export function range(n: unknown): [number, number, number] {
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

export function keepsComments(before: string, after: string, allowed: readonly string[]): boolean {
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

export function readsAs(text: string, v: string | number | boolean): boolean {
  try {
    return parse(text) === v;
  } catch {
    return false;
  }
}

export function blockScalar(v: string | number | boolean, style?: Scalar.Type): string {
  if (typeof v !== "string") return String(v);
  if (style === "QUOTE_SINGLE") return `'${v.replaceAll("'", "''")}'`;
  if (style === "QUOTE_DOUBLE") return JSON.stringify(v);
  const plain = new Document(v).toString({ lineWidth: 0 }).replace(/\n$/, "");
  return !plain.includes("\n") && readsAs(plain, v) ? plain : JSON.stringify(v);
}

export function flowScalar(v: string | number | boolean): string {
  if (typeof v !== "string") return String(v);
  return FLOW_PLAIN.test(v) && readsAs(v, v) ? v : JSON.stringify(v);
}

export function flowList(values: readonly string[]): string {
  return `[${values.map((v) => flowScalar(v)).join(", ")}]`;
}

function flowMapOfLists(m: Readonly<Record<string, readonly string[]>>): string {
  return `{${Object.entries(m)
    .map(([k, vs]) => `${flowScalar(k)}: ${flowList(vs)}`)
    .join(", ")}}`;
}

/** Text written as it is: what the editor composes itself (`{from: subject}`). */
export interface Raw {
  raw: string;
}
export type Inner = Value | Raw;

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
export function valueText(v: Inner): string {
  if (isRaw(v)) return v.raw;
  if (isList(v)) return flowList(v);
  if (isMapValue(v)) return flowMapOfLists(v);
  return blockScalar(v);
}

/** New keys, nested from a path's missing tail, in block style at an indent. */
export function blockPairs(keys: readonly string[], v: Inner, indent: string): string {
  const [head, ...rest] = keys;
  if (head === undefined) throw new Refused("structure");
  if (rest.length === 0) return `${indent}${flowScalar(head)}: ${valueText(v)}\n`;
  return `${indent}${flowScalar(head)}:\n${blockPairs(rest, v, indent + "  ")}`;
}

export function nodeAt(d: YamlDocument, path: Path): unknown {
  return d.doc.getIn(path, true);
}

export function hasComment(src: string, from: number, to: number): boolean {
  return commentsOf(src.slice(from, to)).length > 0;
}

// --- set --------------------------------------------------------------------

export function setAt(d: YamlDocument, path: Path, value: Inner | undefined): string {
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

function replaceValue(d: YamlDocument, path: Path, node: unknown, value: Inner): string {
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
    for (const k of existing) if (!(k in value)) s = setAt(reparse(d, s), [...path, k], undefined);
    for (const [k, vs] of Object.entries(value)) s = setAt(reparse(d, s), [...path, k], vs);
    return s;
  }
  throw new Refused("structure");
}

function inFlow(d: YamlDocument, path: Path): boolean {
  for (let i = path.length - 1; i > 0; i--) {
    const n = nodeAt(d, path.slice(0, i));
    if ((isMap(n) || isSeq(n)) && n.flow) return true;
  }
  return false;
}

/** A block list set item by item: removed items go with their own lines, new ones are appended. */
function setBlockList(d: YamlDocument, path: Path, node: YAMLSeq, value: readonly string[]): string {
  let s = d.source;
  const have = node.items.map((i) => str(i));
  // Remove from the end, so earlier items keep their place.
  for (let i = have.length - 1; i >= 0; i--) {
    const v = have[i];
    if (v === undefined || !value.includes(v)) s = removeItem(reparse(d, s), path, i);
  }
  const kept = new Set(have.filter((v): v is string => v !== undefined && value.includes(v)));
  for (const v of value) if (!kept.has(v)) s = appendItem(reparse(d, s), path, v);
  return s;
}

export function insertKey(src: string, parent: unknown, keys: readonly string[], value: Inner): string {
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
export function insertUnderEmptyKey(src: string, pair: { key: unknown; value: unknown }, keys: readonly string[], value: Inner): string {
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

/** Removes a key; a map it leaves empty goes too, up to (never including) one the document keeps. */
export function removeKey(d: YamlDocument, path: Path): string {
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
  // A map the document's reader keeps (a policy's rule) is never emptied away by a field's removal.
  if (parent.items.length === 1 && parentPath.length > 0 && !d.keep?.(parentPath)) {
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
export function keptComments(src: string, from: number, to: number): string {
  const lines: string[] = [];
  const text = src.slice(from, to);
  if (commentsOf(text).length === 0) return "";
  const indent = " ".repeat(column(src, from + (text.length - text.trimStart().length)));
  for (const c of commentsOf(text)) lines.push(`${indent}${c}\n`);
  return lines.join("");
}

// --- sequences --------------------------------------------------------------

/** The text of a block list's item: from its `-` line (its comments above it with it) to the end of its last line. */
export function itemSpan(src: string, seq: YAMLSeq, i: number, withHeader: boolean): { start: number; end: number; dash: number } {
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

export function removeItem(d: YamlDocument, path: Path, i: number): string {
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

export function appendItem(d: YamlDocument, path: Path, v: string | Readonly<Record<string, string>>): string {
  const src = d.source;
  const seq = nodeAt(d, path);
  if (seq === undefined || seq === null || (isScalar(seq) && seq.value === null)) {
    // No list yet: it is created holding this one item.
    if (typeof v === "string") return setAt(d, path, [v]);
    return createMapList(d, path, v);
  }
  if (!isSeq(seq)) throw new Refused("structure");
  // `key: []` takes a first map as a block list, as a key holding nothing does.
  if (seq.flow && seq.items.length === 0 && typeof v !== "string") return createMapList(d, path, v);
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

export function mapItem(v: Readonly<Record<string, string>>, indent: string): string {
  return Object.entries(v)
    .map(([k, x], i) => `${i === 0 ? `${indent}- ` : `${indent}  `}${flowScalar(k)}: ${blockScalar(x)}\n`)
    .join("");
}

/** A list of maps created under a key that has none (`obligations:`, `credentials:`). */
export function createMapList(d: YamlDocument, path: Path, v: Readonly<Record<string, string>>): string {
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


// --- a list of maps, item by item ---------------------------------------------

/** The block list at a path, or a refusal: a flow list (`[...]`) is edited in source. */
export function blockSeq(d: YamlDocument, path: Path): YAMLSeq {
  const seq = nodeAt(d, path);
  if (!isSeq(seq) || seq.flow) throw new Refused("structure");
  return seq;
}

export interface Layout {
  head: string;
  spans: string[];
  gaps: string[];
  tail: string;
}

/** A block list cut into each item's own lines (its header comments with it) and the gaps between them. */
export function layoutOf(d: YamlDocument, path: Path): Layout {
  const src = d.source;
  const seq = blockSeq(d, path);
  const spans = seq.items.map((_, i) => itemSpan(src, seq, i, true));
  const first = spans[0];
  const last = spans[spans.length - 1];
  if (!first || !last) throw new Refused("structure");
  const gaps: string[] = [];
  for (let i = 0; i + 1 < spans.length; i++) gaps.push(src.slice((spans[i] as { end: number }).end, (spans[i + 1] as { start: number }).start));
  return { head: src.slice(0, first.start), spans: spans.map((s) => src.slice(s.start, s.end)), gaps, tail: src.slice(last.end) };
}

export function joinLayout(l: Layout): string {
  let out = l.head;
  l.spans.forEach((s, i) => {
    out += s;
    if (i < l.spans.length - 1) out += l.gaps[i] ?? "";
  });
  return out + l.tail;
}

export function withNewline(s: string): string {
  return s.endsWith("\n") ? s : s + "\n";
}

/** Moves an item of a block list to another place: its lines and comments travel, the gaps stay where they were. */
export function moveItem(d: YamlDocument, path: Path, from: number, to: number): string {
  const l = layoutOf(d, path);
  if (from === to || from < 0 || to < 0 || from >= l.spans.length || to >= l.spans.length) return d.source;
  // The last item's lines may end the file without a newline; inside the list they need one.
  const lastHadNewline = (l.spans[l.spans.length - 1] as string).endsWith("\n");
  const spans = l.spans.map(withNewline);
  const [moved] = spans.splice(from, 1);
  spans.splice(to, 0, moved as string);
  if (!lastHadNewline && l.tail === "") spans[spans.length - 1] = (spans[spans.length - 1] as string).replace(/\n$/, "");
  return joinLayout({ ...l, spans });
}

/**
 * Removes an item of a block list with the comments written above it and
 * inside it. The only item leaves the key holding an empty list (`key: []`),
 * so the key a reader looks for is still there.
 */
export function removeSpan(d: YamlDocument, path: Path, index: number): string {
  const l = layoutOf(d, path);
  if (index < 0 || index >= l.spans.length) return d.source;
  const key = path[path.length - 1];
  if (l.spans.length === 1) {
    if (typeof key !== "string" || !/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) throw new Refused("structure");
    // `key:` ending the head takes `[]`; a key with a comment after it is left holding nothing, which reads as no list.
    return l.head.replace(new RegExp(`${key}:[ \\t]*\\n$`), `${key}: []\n`) + l.tail;
  }
  const spans = [...l.spans];
  const gaps = [...l.gaps];
  const wasLast = index === spans.length - 1;
  spans.splice(index, 1);
  gaps.splice(wasLast ? index - 1 : index, 1);
  if (wasLast && !(l.spans[index] as string).endsWith("\n")) spans[spans.length - 1] = (spans[spans.length - 1] as string).replace(/\n$/, "");
  return joinLayout({ ...l, spans, gaps });
}

/** The comments written with an item — above it and inside it — which its removal takes. */
export function spanComments(d: YamlDocument, path: Path, index: number): string[] {
  try {
    const seq = blockSeq(d, path);
    const { start, end } = itemSpan(d.source, seq, index, true);
    return commentsOf(d.source.slice(start, end));
  } catch {
    return [];
  }
}
