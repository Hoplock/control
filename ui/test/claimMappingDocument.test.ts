// The claim mapping as a document (0068): every byte survives a round trip,
// every edit is the smallest change to the text that says it and loses no
// comment, and each typed name is held to the server's rules.

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  applyMappingEdit,
  checkMappingField,
  mappingChanges,
  mappingOf,
  parseMapping,
  rejectionTarget,
  removedComments,
  serialise,
  type MappingEdit,
  type VisualMapping,
} from "../src/screens/claimMappingDocument";
import { commentsOf } from "../src/screens/yamlDocument";
import rules from "../src/screens/claimMappingRules.json";

const UI = join(import.meta.dirname, "..");
const corpus = readFileSync(join(UI, "test", "mapping-corpus.yaml"), "utf8");
// The fixture's mapping, read from its source: the module that holds it
// drives a browser, which a unit test does not load.
const standing = readFileSync(join(UI, "e2e", "fixture", "standing.ts"), "utf8");
const fixture = /export const MAPPING = `([^`]*)`;/.exec(standing)?.[1] as string;

function view(source: string): VisualMapping {
  const v = mappingOf(parseMapping(source));
  if (typeof v === "string") throw new Error(`not visual: ${v}`);
  return v;
}

function edited(source: string, e: MappingEdit): string {
  const a = applyMappingEdit(parseMapping(source), e);
  if (a.kind !== "ok") throw new Error(`refused: ${a.reason}`);
  return a.source;
}

/** The lines an edit took out and put in, between the longest common prefix and suffix. */
function lineDiff(before: string, after: string): { removed: string[]; added: string[] } {
  const a = before.split("\n");
  const b = after.split("\n");
  let p = 0;
  while (p < a.length && p < b.length && a[p] === b[p]) p++;
  let s = 0;
  while (s < a.length - p && s < b.length - p && a[a.length - 1 - s] === b[b.length - 1 - s]) s++;
  return { removed: a.slice(p, a.length - s), added: b.slice(p, b.length - s) };
}

function sortedComments(s: string): string[] {
  return [...commentsOf(s)].sort();
}

describe("a round trip", () => {
  it("is byte-identical for the fixture's mapping and the corpus", () => {
    expect(fixture).toContain("Payments Engineering");
    for (const source of [fixture, corpus]) {
      const d = parseMapping(source);
      expect(serialise(d)).toBe(source);
      expect(serialise(parseMapping(serialise(d)))).toBe(source);
    }
  });

  it("keeps CRLF, odd spacing and a missing final newline", () => {
    for (const s of [corpus.replaceAll("\n", "\r\n"), corpus.replace(/\n$/, ""), corpus.replaceAll(": ", ":    ")]) {
      expect(serialise(parseMapping(s))).toBe(s);
    }
  });
});

describe("reading", () => {
  it("reads the corpus's claims and entries in the document's order", () => {
    const v = view(corpus);
    expect(v.schemaVersion).toBe("1");
    expect(v.description).toBe("Corp and lab, scoped apart: reviewed in CHG-1182");
    expect(v.claims.map((c) => [c.claim, c.attribute, c.values])).toEqual([
      ["dept", "department", ["engineering", "operations"]],
      ["email", "email", null],
      ["cost_center", "finance.cost_center", ["4100", "4200"]],
    ]);
    expect(v.claimsEditable).toBe(true);
    expect(v.groups?.claim).toBe("groups");
    expect(v.groups?.requireDirectories).toBe(true);
    expect(v.groups?.entries.map((e) => [e.external, e.group, e.directories])).toEqual([
      ["Payments Engineering", "payments-eng", null],
      ["Payments Engineering", "payments-eng", ["corp"]],
      ["Payments Engineering", "payments-lab", ["lab", "lab-2"]],
      ["sre", "sre", null],
    ]);
    expect(v.unmodelled).toEqual([]);
  });

  it("groups the entries by the name they read, each name where it first appears", () => {
    const names = view(corpus).groups?.names ?? [];
    expect(names.map((n) => [n.external, n.entries.map((e) => e.index)])).toEqual([
      ["Payments Engineering", [0, 1, 2]],
      ["sre", [3]],
    ]);
  });

  it("lists a key it does not model, at its line, and drops nothing", () => {
    const v = view("schema_version: 1\nnotes: kept\nclaims:\n  - claim: a\n    attribute: a\n    trust: high\n");
    expect(v.unmodelled).toEqual([{ key: "notes", line: 2 }]);
    expect(v.claims[0]?.unmodelled).toEqual([{ key: "trust", line: 6 }]);
  });

  it("says a document that is not YAML, or not a map, is not visual", () => {
    expect(mappingOf(parseMapping("claims: [a\n"))).toBe("not_yaml");
    expect(mappingOf(parseMapping("- a\n"))).toBe("not_mapping");
  });

  it("reads a flow list of claims but offers no structure edit on it", () => {
    const v = view("schema_version: 1\nclaims: [{claim: a, attribute: a}]\n");
    expect(v.claims[0]?.attribute).toBe("a");
    expect(v.claimsEditable).toBe(false);
  });
});

describe("an edit", () => {
  const cases: { name: string; edit: MappingEdit; removed: string[]; added: string[] }[] = [
    {
      name: "a claim's attribute",
      edit: { op: "attribute", index: 1, attribute: "mail" },
      removed: ["    attribute: email"],
      added: ["    attribute: mail"],
    },
    {
      name: "a claim's values, in flow style",
      edit: { op: "values", index: 0, values: ["engineering", "operations", "support"] },
      removed: ["    values: [engineering, operations]   # anything else maps nothing"],
      added: ["    values: [engineering, operations, support]   # anything else maps nothing"],
    },
    {
      name: "a claim's values, in block style",
      edit: { op: "values", index: 2, values: ["4100"] },
      removed: ["      - '4200'"],
      added: [],
    },
    {
      name: "the description",
      edit: { op: "description", value: "Reviewed again" },
      removed: ['description: "Corp and lab, scoped apart: reviewed in CHG-1182"'],
      added: ['description: "Reviewed again"'],
    },
    {
      name: "an entry's group",
      edit: { op: "entryGroup", index: 3, group: "site-reliability" },
      removed: ["      group: sre"],
      added: ["      group: site-reliability"],
    },
    {
      name: "an entry's directories",
      edit: { op: "entryDirectories", index: 1, directories: ["corp", "lab-3"] },
      removed: ["      directories: [corp]   # the corporate directory"],
      added: ["      directories: [corp, lab-3]   # the corporate directory"],
    },
    {
      name: "scoping an unscoped entry",
      edit: { op: "entryDirectories", index: 3, directories: ["corp"] },
      removed: [],
      added: ["      directories: [corp]"],
    },
    {
      name: "require_directories off",
      edit: { op: "requireDirectories", on: false },
      removed: ["  require_directories: true"],
      added: [],
    },
  ];
  for (const c of cases) {
    it(`changes only ${c.name}, and loses no comment`, () => {
      const after = edited(corpus, c.edit);
      expect(lineDiff(corpus, after)).toEqual({ removed: c.removed, added: c.added });
      expect(sortedComments(after)).toEqual(sortedComments(corpus));
    });
  }

  it("removes no values but the key, which permits any value", () => {
    const after = edited(corpus, { op: "values", index: 0, values: [] });
    expect(view(after).claims[0]?.values).toBeNull();
    // The comment on the removed line is kept as a line of its own.
    expect(sortedComments(after)).toEqual(sortedComments(corpus));
  });

  it("adds a claim with two permitted values at the end of the list", () => {
    const after = edited(corpus, { op: "addClaim", claim: "title", attribute: "title", values: ["staff", "lead"] });
    const { removed, added } = lineDiff(corpus, after);
    expect(removed).toEqual([]);
    expect(added).toEqual(["  - claim: title", "    attribute: title", "    values: [staff, lead]"]);
    expect(view(after).claims.at(-1)).toMatchObject({ claim: "title", attribute: "title", values: ["staff", "lead"] });
  });

  it("adds an entry scoped to a directory", () => {
    const after = edited(corpus, { op: "addEntry", external: "Payments Engineering", group: "payments-lab", directories: ["lab-3"] });
    expect(lineDiff(corpus, after).added).toEqual(["    - external: Payments Engineering", "      group: payments-lab", "      directories: [lab-3]"]);
    expect(view(after).groups?.names[0]?.entries).toHaveLength(4);
  });

  it("starts a claims list and a group mapping in a document that has neither", () => {
    let s = "schema_version: 1\n";
    s = edited(s, { op: "addClaim", claim: "dept", attribute: "department" });
    s = edited(s, { op: "groupClaim", claim: "groups" });
    s = edited(s, { op: "addEntry", external: "sre", group: "sre" });
    expect(s).toBe("schema_version: 1\nclaims:\n  - claim: dept\n    attribute: department\ngroups:\n  claim: groups\n  map:\n    - external: sre\n      group: sre\n");
  });

  it("takes a removed row's own comments with it, counts them, and leaves the others", () => {
    const d = parseMapping(corpus);
    expect(removedComments(d, { op: "removeClaim", index: 0 })).toBe(2);
    const after = edited(corpus, { op: "removeClaim", index: 0 });
    expect(view(after).claims.map((c) => c.attribute)).toEqual(["email", "finance.cost_center"]);
    const gone = sortedComments(corpus).filter((c) => !sortedComments(after).includes(c));
    expect(gone).toEqual(["# Who someone works for, trusted only for these two values.", "# anything else maps nothing"].sort());
  });

  it("leaves an empty list when the last row goes, and can add to it again", () => {
    let s = "schema_version: 1\nclaims:\n  - claim: a\n    attribute: a\n";
    s = edited(s, { op: "removeClaim", index: 0 });
    expect(s).toBe("schema_version: 1\nclaims: []\n");
    s = edited(s, { op: "addClaim", claim: "b", attribute: "b" });
    expect(view(s).claims.map((c) => c.attribute)).toEqual(["b"]);
  });

  it("moves every claim to every place and back, byte-identical", () => {
    const n = view(corpus).claims.length;
    for (let from = 0; from < n; from++) {
      for (let to = 0; to < n; to++) {
        const moved = edited(corpus, { op: "moveClaim", from, to });
        expect(sortedComments(moved)).toEqual(sortedComments(corpus));
        expect(edited(moved, { op: "moveClaim", from: to, to: from })).toBe(corpus);
      }
    }
  });

  it("refuses a structure edit on a flow list rather than rewriting it", () => {
    const a = applyMappingEdit(parseMapping("schema_version: 1\nclaims: [{claim: a, attribute: a}]\n"), { op: "removeClaim", index: 0 });
    expect(a).toEqual({ kind: "refused", reason: "structure" });
  });
});

describe("a typed name", () => {
  it("is held to the server's own rules, with the server's codes", () => {
    expect(checkMappingField("claim", " ")).toBe("mapping.claim_empty");
    expect(checkMappingField("claim", "Any Claim Name")).toBeNull();
    expect(checkMappingField("attribute", "department")).toBeNull();
    expect(checkMappingField("attribute", "Department")).toBe("mapping.attribute_name");
    expect(checkMappingField("attribute", "finance.cost_center")).toBeNull();
    for (const name of rules.reserved.names) expect(checkMappingField("attribute", name)).toBe("mapping.attribute_reserved");
    expect(checkMappingField("attribute", `${rules.reserved.prefix}proxy`)).toBe("mapping.attribute_reserved");
    expect(checkMappingField("group", "payments-eng")).toBeNull();
    expect(checkMappingField("group", "-eng")).toBe("mapping.group_name");
    expect(checkMappingField("external", "Payments Engineering")).toBeNull();
    expect(checkMappingField("directory", "corp")).toBeNull();
    expect(checkMappingField("directory", "Corp")).toBe("mapping.directory_name");
    expect(checkMappingField("directory", " corp")).toBe("mapping.directory_name");
    expect(checkMappingField("value", " ")).toBe("mapping.value_empty");
  });

  it("is checked with every field the rules file names, and nothing else", () => {
    // Every field the generated file carries is one the editor checks.
    expect(Object.keys(rules.fields).sort()).toEqual(["attribute", "claim", "directory", "external", "group", "group_claim", "value"]);
  });
});

describe("a rejection", () => {
  it("goes to the row and field its path names", () => {
    expect(rejectionTarget(undefined)).toEqual({ kind: "document" });
    expect(rejectionTarget("schema_version")).toEqual({ kind: "schema_version" });
    expect(rejectionTarget("claims[2].attribute")).toEqual({ kind: "claim", index: 2, key: "attribute" });
    expect(rejectionTarget("claims[0].values[1]")).toEqual({ kind: "claim", index: 0, key: "values", value: 1 });
    expect(rejectionTarget("groups.claim")).toEqual({ kind: "groups", key: "claim" });
    expect(rejectionTarget("groups.map[2].directories[0]")).toEqual({ kind: "entry", index: 2, key: "directories", value: 0 });
    expect(rejectionTarget("groups.map[1].directories")).toEqual({ kind: "entry", index: 1, key: "directories" });
    expect(rejectionTarget("groups.map[3].external")).toEqual({ kind: "entry", index: 3, key: "external" });
    expect(rejectionTarget("something.else")).toEqual({ kind: "document" });
  });
});

describe("what an edit changes", () => {
  it("is said row by row against the active version", () => {
    const before = view(fixture);
    let s = edited(fixture, { op: "addClaim", claim: "title", attribute: "title", values: ["staff", "lead"] });
    s = edited(s, { op: "entryGroup", index: 2, group: "site-reliability" });
    s = edited(s, { op: "addEntry", external: "sre", group: "lab-sre", directories: ["lab"] });
    s = edited(s, { op: "removeEntry", index: 0 });
    const changes = mappingChanges(before, view(s), true);
    expect(changes.map((c) => c.kind)).toEqual(["claimAdded", "entryChanged", "entryAdded", "entryRemoved"]);
  });

  it("names a reorder, and never says nothing changed over a changed document", () => {
    const before = view(corpus);
    const moved = edited(corpus, { op: "moveClaim", from: 0, to: 2 });
    expect(mappingChanges(before, view(moved), true)).toEqual([{ kind: "claimsReordered" }]);
    const respaced = corpus.replace("schema_version: 1   #", "schema_version: 1 #");
    expect(mappingChanges(before, view(respaced), true)).toEqual([{ kind: "unmodelled" }]);
    expect(mappingChanges(before, view(corpus), false)).toEqual([]);
  });

  it("names a change of scope as one entry removed and another added", () => {
    const before = view(fixture);
    const after = view(edited(fixture, { op: "entryDirectories", index: 2, directories: ["corp"] }));
    expect(mappingChanges(before, after, true).map((c) => c.kind)).toEqual(["entryAdded", "entryRemoved"]);
  });
});
