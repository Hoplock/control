// A CLAIM MAPPING AS A DOCUMENT (0068; PLAN M7, §6 "Federation (M7)" and "The
// directory door"). The visual claim-mapping editor works on the document an
// operator wrote, never on a copy re-serialised from a model of it: a mapping
// version IS its document and that document's digest, exactly as a policy
// version is its source (0066). So this module is 0066's text-splice core
// (yamlDocument.ts) applied to the mapping's schema (`schema_version` 1,
// internal/identity/mapping.go):
//
//   - `mappingOf` reads what the visual view shows: the claims that become
//     attributes, in the document's order, each with its permitted values; and
//     the group entries, each with the directories it is scoped to — grouped
//     by the external name they read, so the entry that serves sign-ins and
//     every entry scoped to a directory are seen together for one name;
//   - `applyMappingEdit` makes each edit the smallest change to the text that
//     says it, through the core, which refuses one that would lose a comment;
//   - `checkMappingField` holds a typed name to the server's own pattern and
//     reserved set, read from claimMappingRules.json, which a Go test
//     generates from internal/identity and holds equal to it; no pattern or
//     reserved name is written here;
//   - `mappingChanges` says, row by row, what an edited document adds, removes
//     and changes against the active one.
//
// The server is the authority: what this module checks, ParseMapping checks
// again on every dry run and write, and its rejections render on the field
// their `field` path names (`rejectionTarget`). A key the view does not model
// is listed (`unmodelled`) and shown read-only with "edit in source", never
// dropped — though a mapping is read strictly, so the server refuses one
// (`mapping.unparseable`) and the view says that too. Pure: no React, no DOM.

import { isMap, isScalar, isSeq } from "yaml";
import rules from "./claimMappingRules.json";
import {
  appendItem,
  attempt,
  keyOf,
  lineOf,
  moveItem,
  nodeAt,
  parseYaml,
  Refused,
  removeSpan,
  reparse,
  serialise,
  setAt,
  spanComments,
  startOf,
  str,
  strings,
  type Applied,
  type YamlDocument,
} from "./yamlDocument";

export { serialise, type Applied };

export type MappingDocument = YamlDocument;

export function parseMapping(source: string): MappingDocument {
  return parseYaml(source);
}

// ---------------------------------------------------------------------------
// Reading: what the visual view shows.
// ---------------------------------------------------------------------------

/** A key the view does not model: shown read-only with "edit in source" at its line. */
export interface Unmodelled {
  key: string;
  line: number;
}

/** One `claims` entry: the claim it reads, the attribute it produces, and its permitted values (`null`: any value). */
export interface ClaimRow {
  index: number;
  line: number;
  claim: string;
  attribute: string;
  values: readonly string[] | null;
  unmodelled: readonly Unmodelled[];
}

/** One `groups.map` entry: the name it reads, the local group it produces, and the directories it is scoped to (`null`: unscoped). */
export interface EntryRow {
  index: number;
  line: number;
  external: string;
  group: string;
  directories: readonly string[] | null;
  unmodelled: readonly Unmodelled[];
}

/** The entries that read one external name, in the document's order: the unscoped one (if any) and every scoped one. */
export interface NameGroup {
  external: string;
  entries: readonly EntryRow[];
}

export interface GroupsView {
  line: number;
  /** `groups.claim`: the claim a sign-in's groups are read from. */
  claim: string;
  requireDirectories: boolean;
  entries: readonly EntryRow[];
  /** The entries grouped by the external name they read, in the order each name first appears. */
  names: readonly NameGroup[];
  /** Whether entries can be added and removed: `map` is a block list, or absent. */
  editable: boolean;
  unmodelled: readonly Unmodelled[];
}

export interface VisualMapping {
  schemaVersion: string | undefined;
  description: string | undefined;
  claims: readonly ClaimRow[];
  /** Whether claims can be added, removed and moved: `claims` is a block list, or absent. */
  claimsEditable: boolean;
  /** Absent: the mapping maps no group. */
  groups: GroupsView | null;
  unmodelled: readonly Unmodelled[];
}

export type NotVisual = "not_yaml" | "not_mapping";

const TOP_MODELLED = new Set(["schema_version", "description", "claims", "groups"]);
const CLAIM_MODELLED = new Set(["claim", "attribute", "values"]);
const GROUPS_MODELLED = new Set(["claim", "require_directories", "map"]);
const ENTRY_MODELLED = new Set(["external", "group", "directories"]);

function unmodelledOf(src: string, node: unknown, modelled: ReadonlySet<string>): Unmodelled[] {
  if (!isMap(node)) return [];
  const out: Unmodelled[] = [];
  for (const p of node.items) {
    const k = keyOf(p);
    if (k !== undefined && !modelled.has(k)) out.push({ key: k, line: lineOf(src, startOf((p as { key: unknown }).key) ?? 0) });
  }
  return out;
}

/** Whether a list can take structure edits: absent, empty, or a block list. */
function blockOrAbsent(n: unknown): boolean {
  if (n === undefined || n === null) return true;
  if (isScalar(n)) return n.value === null;
  return isSeq(n) && (!n.flow || n.items.length === 0);
}

export function mappingOf(d: MappingDocument): VisualMapping | NotVisual {
  if (!d.ok) return d.doc.errors.length ? "not_yaml" : "not_mapping";
  const src = d.source;
  const top = d.doc.contents;
  const line = (n: unknown) => lineOf(src, startOf(n) ?? 0);

  const claimsNode = nodeAt(d, ["claims"]);
  const claims: ClaimRow[] = isSeq(claimsNode)
    ? claimsNode.items.map((item, index) => {
        const values = isMap(item) ? item.get("values", true) : undefined;
        return {
          index,
          line: line(item),
          claim: (isMap(item) ? str(item.get("claim", true)) : undefined) ?? "",
          attribute: (isMap(item) ? str(item.get("attribute", true)) : undefined) ?? "",
          values: values === undefined || values === null || (isScalar(values) && values.value === null) ? null : strings(values),
          unmodelled: unmodelledOf(src, item, CLAIM_MODELLED),
        };
      })
    : [];

  const groupsNode = nodeAt(d, ["groups"]);
  let groups: GroupsView | null = null;
  if (isMap(groupsNode)) {
    const mapNode = groupsNode.get("map", true);
    const entries: EntryRow[] = isSeq(mapNode)
      ? mapNode.items.map((item, index) => {
          const dirs = isMap(item) ? item.get("directories", true) : undefined;
          return {
            index,
            line: line(item),
            external: (isMap(item) ? str(item.get("external", true)) : undefined) ?? "",
            group: (isMap(item) ? str(item.get("group", true)) : undefined) ?? "",
            directories: dirs === undefined || dirs === null || (isScalar(dirs) && dirs.value === null) ? null : strings(dirs),
            unmodelled: unmodelledOf(src, item, ENTRY_MODELLED),
          };
        })
      : [];
    const require = groupsNode.get("require_directories", true);
    groups = {
      line: line(groupsNode),
      claim: str(groupsNode.get("claim", true)) ?? "",
      requireDirectories: isScalar(require) && require.value === true,
      entries,
      names: namesOf(entries),
      editable: blockOrAbsent(mapNode),
      unmodelled: unmodelledOf(src, groupsNode, GROUPS_MODELLED),
    };
  }

  return {
    schemaVersion: str(nodeAt(d, ["schema_version"])),
    description: str(nodeAt(d, ["description"])),
    claims,
    claimsEditable: blockOrAbsent(claimsNode),
    groups,
    unmodelled: isMap(top) ? unmodelledOf(src, top, TOP_MODELLED) : [],
  };
}

/** Entries grouped by the external name they read, in the order each name first appears; the order within a name is the document's. */
export function namesOf(entries: readonly EntryRow[]): NameGroup[] {
  const byName = new Map<string, EntryRow[]>();
  for (const e of entries) {
    const list = byName.get(e.external);
    if (list) list.push(e);
    else byName.set(e.external, [e]);
  }
  return [...byName].map(([external, rows]) => ({ external, entries: rows }));
}

// ---------------------------------------------------------------------------
// Writing.
// ---------------------------------------------------------------------------

export type MappingEdit =
  /** The operator's note; empty removes the key. */
  | { op: "description"; value: string }
  | { op: "addClaim"; claim: string; attribute: string; values?: readonly string[] }
  /** Removes a claim with the comments written with it. */
  | { op: "removeClaim"; index: number }
  | { op: "moveClaim"; from: number; to: number }
  | { op: "claim"; index: number; claim: string }
  | { op: "attribute"; index: number; attribute: string }
  /** The permitted values; none removes the key, which permits any value. */
  | { op: "values"; index: number; values: readonly string[] }
  /** `groups.claim`; writing it where the mapping maps no group starts a group mapping. */
  | { op: "groupClaim"; claim: string }
  /** On writes `require_directories: true`; off removes the key, which is the default. */
  | { op: "requireDirectories"; on: boolean }
  | { op: "addEntry"; external: string; group: string; directories?: readonly string[] }
  /** Removes an entry with the comments written with it. */
  | { op: "removeEntry"; index: number }
  | { op: "entryGroup"; index: number; group: string }
  /** The directories an entry is scoped to; none removes the key, which makes it unscoped. */
  | { op: "entryDirectories"; index: number; directories: readonly string[] };

const CLAIMS = ["claims"] as const;
const MAP = ["groups", "map"] as const;

export function applyMappingEdit(d: MappingDocument, e: MappingEdit): Applied {
  const allowed = e.op === "removeClaim" ? spanComments(d, CLAIMS, e.index) : e.op === "removeEntry" ? spanComments(d, MAP, e.index) : [];
  return attempt(d, () => mappingEdit(d, e), allowed);
}

/** The comments a removal takes with it, which its announcement counts. */
export function removedComments(d: MappingDocument, e: MappingEdit): number {
  if (e.op === "removeClaim") return spanComments(d, CLAIMS, e.index).length;
  if (e.op === "removeEntry") return spanComments(d, MAP, e.index).length;
  return 0;
}

function trimmedOrUndefined(v: string): string | undefined {
  const t = v.trim();
  return t === "" ? undefined : t;
}

function mappingEdit(d: MappingDocument, e: MappingEdit): string {
  switch (e.op) {
    case "description":
      return setAt(d, ["description"], e.value === "" ? undefined : e.value);
    case "addClaim": {
      const s = appendItem(d, CLAIMS, { claim: e.claim, attribute: e.attribute });
      if (!e.values || e.values.length === 0) return s;
      const next = reparse(d, s);
      const seq = nodeAt(next, CLAIMS);
      if (!isSeq(seq)) throw new Refused("structure");
      return setAt(next, [...CLAIMS, seq.items.length - 1, "values"], e.values);
    }
    case "removeClaim":
      return removeSpan(d, CLAIMS, e.index);
    case "moveClaim":
      return moveItem(d, CLAIMS, e.from, e.to);
    case "claim":
      return setAt(d, [...CLAIMS, e.index, "claim"], e.claim);
    case "attribute":
      return setAt(d, [...CLAIMS, e.index, "attribute"], e.attribute);
    case "values":
      return setAt(d, [...CLAIMS, e.index, "values"], e.values);
    case "groupClaim":
      return setAt(d, ["groups", "claim"], trimmedOrUndefined(e.claim) ?? e.claim);
    case "requireDirectories":
      return setAt(d, ["groups", "require_directories"], e.on ? true : undefined);
    case "addEntry": {
      if (!isMap(nodeAt(d, ["groups"]))) throw new Refused("structure");
      const s = appendItem(d, MAP, { external: e.external, group: e.group });
      if (!e.directories || e.directories.length === 0) return s;
      const next = reparse(d, s);
      const seq = nodeAt(next, MAP);
      if (!isSeq(seq)) throw new Refused("structure");
      return setAt(next, [...MAP, seq.items.length - 1, "directories"], e.directories);
    }
    case "removeEntry":
      return removeSpan(d, MAP, e.index);
    case "entryGroup":
      return setAt(d, [...MAP, e.index, "group"], e.group);
    case "entryDirectories":
      return setAt(d, [...MAP, e.index, "directories"], e.directories);
  }
}

// ---------------------------------------------------------------------------
// Checked as typed, with the server's rules (claimMappingRules.json).
// ---------------------------------------------------------------------------

/** A field of a mapping document a name is typed into. */
export type MappingField = keyof typeof rules.fields;

interface FieldRule {
  trimmed: boolean;
  required: string;
  pattern?: string;
  invalid?: string;
  reserved?: string;
}

const PATTERNS: Readonly<Record<string, RegExp>> = Object.fromEntries(Object.entries(rules.patterns).map(([k, p]) => [k, new RegExp(p)]));

/** Whether a name is one this server sets for itself, which no mapping may produce. */
export function reservedName(name: string): boolean {
  return rules.reserved.names.includes(name) || name.startsWith(rules.reserved.prefix);
}

/**
 * The code the server would refuse a typed name with, or null. It is the
 * same code `ParseMapping` answers, so the field shows the server's sentence
 * for it (claimMappingCodes.ts); duplicates across rows are the server's to
 * say, on the dry run.
 */
export function checkMappingField(field: MappingField, value: string): string | null {
  const rule: FieldRule = rules.fields[field];
  const v = rule.trimmed ? value.trim() : value;
  if (v === "") return rule.required;
  if (rule.pattern && !(PATTERNS[rule.pattern] as RegExp).test(v)) return rule.invalid ?? rule.required;
  if (rule.reserved && reservedName(v)) return rule.reserved;
  return null;
}

/** The value as the server reads it: trimmed where the server trims the field. */
export function asWritten(field: MappingField, value: string): string {
  return rules.fields[field].trimmed ? value.trim() : value;
}

/** The one schema version this build reads, which a new mapping is written with. */
export const SCHEMA_VERSION = rules.schema_version;

// ---------------------------------------------------------------------------
// Where a server rejection goes: the row and field its `field` path names.
// ---------------------------------------------------------------------------

export type RejectionTarget =
  | { kind: "document" }
  | { kind: "schema_version" }
  | { kind: "description" }
  | { kind: "claim"; index: number; key: "claim" | "attribute" | "values" | "row"; value?: number }
  | { kind: "groups"; key: "claim" | "require_directories" | "row" }
  | { kind: "entry"; index: number; key: "external" | "group" | "directories" | "row"; value?: number };

/**
 * The row and field a rejection's `field` names: `claims[2].attribute` that
 * claim's attribute, `groups.map[2].directories[0]` that entry's first
 * directory chip, `groups.claim` the group claim. One with no field, or one
 * this console cannot place, heads the view.
 */
export function rejectionTarget(field: string | undefined): RejectionTarget {
  if (!field) return { kind: "document" };
  if (field === "schema_version") return { kind: "schema_version" };
  if (field === "description") return { kind: "description" };
  let m = /^claims\[(\d+)\](?:\.(claim|attribute|values)(?:\[(\d+)\])?)?$/.exec(field);
  if (m) {
    const t: RejectionTarget = { kind: "claim", index: Number(m[1]), key: (m[2] as "claim" | "attribute" | "values" | undefined) ?? "row" };
    return m[3] === undefined ? t : { ...t, value: Number(m[3]) };
  }
  m = /^groups\.(claim|require_directories)$/.exec(field);
  if (m) return { kind: "groups", key: m[1] as "claim" | "require_directories" };
  if (field === "groups" || field === "groups.map") return { kind: "groups", key: "row" };
  m = /^groups\.map\[(\d+)\](?:\.(external|group|directories)(?:\[(\d+)\])?)?$/.exec(field);
  if (m) {
    const t: RejectionTarget = { kind: "entry", index: Number(m[1]), key: (m[2] as "external" | "group" | "directories" | undefined) ?? "row" };
    return m[3] === undefined ? t : { ...t, value: Number(m[3]) };
  }
  return { kind: "document" };
}

// ---------------------------------------------------------------------------
// What an edit changes against the active version, row by row.
// ---------------------------------------------------------------------------

export type MappingChange =
  | { kind: "description"; before: string | undefined; after: string | undefined }
  | { kind: "groupClaim"; before: string | undefined; after: string | undefined }
  | { kind: "requireDirectories"; before: boolean; after: boolean }
  | { kind: "claimAdded"; row: ClaimRow }
  | { kind: "claimRemoved"; row: ClaimRow }
  | { kind: "claimChanged"; before: ClaimRow; after: ClaimRow }
  | { kind: "claimsReordered" }
  | { kind: "entryAdded"; row: EntryRow }
  | { kind: "entryRemoved"; row: EntryRow }
  | { kind: "entryChanged"; before: EntryRow; after: EntryRow }
  | { kind: "unmodelled" };

const sameList = (a: readonly string[] | null, b: readonly string[] | null) =>
  a === b || (a !== null && b !== null && a.length === b.length && a.every((x, i) => x === b[i]));

/** An entry's identity across versions: the name it reads and the directories it is scoped to, as a set. */
function entryKey(e: EntryRow): string {
  return JSON.stringify([e.external, e.directories === null ? null : [...e.directories].sort()]);
}

/** A claim's identity across versions: the attribute it produces, which one claim alone may (`mapping.attribute_duplicate`). */
function claimKey(c: ClaimRow): string {
  return c.attribute;
}

/**
 * What `after` adds, removes and changes against `before` (the active
 * version), row by row: a claim by the attribute it produces, an entry by
 * the name it reads and the directories it is scoped to. A change to a key
 * the view does not model is said once, so the summary never claims "no
 * change" over a document that changed.
 */
export function mappingChanges(before: VisualMapping, after: VisualMapping, sourceChanged: boolean): MappingChange[] {
  const out: MappingChange[] = [];
  if (before.description !== after.description) out.push({ kind: "description", before: before.description, after: after.description });
  const gb = before.groups;
  const ga = after.groups;
  if ((gb?.claim || undefined) !== (ga?.claim || undefined)) out.push({ kind: "groupClaim", before: gb?.claim || undefined, after: ga?.claim || undefined });
  if ((gb?.requireDirectories ?? false) !== (ga?.requireDirectories ?? false)) {
    out.push({ kind: "requireDirectories", before: gb?.requireDirectories ?? false, after: ga?.requireDirectories ?? false });
  }

  const claimsBefore = new Map(before.claims.map((c) => [claimKey(c), c]));
  const claimsAfter = new Map(after.claims.map((c) => [claimKey(c), c]));
  for (const c of after.claims) {
    const was = claimsBefore.get(claimKey(c));
    if (!was) out.push({ kind: "claimAdded", row: c });
    else if (was.claim !== c.claim || !sameList(was.values, c.values)) out.push({ kind: "claimChanged", before: was, after: c });
  }
  for (const c of before.claims) if (!claimsAfter.has(claimKey(c))) out.push({ kind: "claimRemoved", row: c });
  const keptOrder = (cs: readonly ClaimRow[], other: Map<string, ClaimRow>) => cs.map(claimKey).filter((k) => other.has(k));
  const ob = keptOrder(before.claims, claimsAfter);
  const oa = keptOrder(after.claims, claimsBefore);
  if (ob.some((k, i) => k !== oa[i])) out.push({ kind: "claimsReordered" });

  const entriesBefore = new Map((gb?.entries ?? []).map((e) => [entryKey(e), e]));
  const entriesAfter = new Map((ga?.entries ?? []).map((e) => [entryKey(e), e]));
  for (const e of ga?.entries ?? []) {
    const was = entriesBefore.get(entryKey(e));
    if (!was) out.push({ kind: "entryAdded", row: e });
    else if (was.group !== e.group) out.push({ kind: "entryChanged", before: was, after: e });
  }
  for (const e of gb?.entries ?? []) if (!entriesAfter.has(entryKey(e))) out.push({ kind: "entryRemoved", row: e });

  if (out.length === 0 && sourceChanged) out.push({ kind: "unmodelled" });
  return out;
}
