// THE VISUAL EDITOR'S DOCUMENT MODEL (0066; PLAN §5.6). A version is its
// source and that source's digest, and a reviewer reads the diff, so
//   1. a round trip is lossless: every bundle in the browser fixture and the
//      corpus that uses every key of the schema (./policy-corpus.yaml, which
//      internal/authoring compiles and walks) loads into the visual view and
//      writes back byte-identical;
//   2. an edit changes only what it says — a value where it stands, a key
//      after its last sibling, a rule moved as its own lines — and keeps every
//      comment, or is refused;
//   3. what the view does not model is listed, never dropped.

import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  applyEdit,
  checkCidr,
  checkHostPattern,
  checkReason,
  checkRuleId,
  applyEdits,
  commentsOf,
  freshRuleId,
  parsePolicy,
  ruleAtLine,
  serialise,
  visualOf,
  type Edit,
  type VisualBundle,
} from "../src/screens/policyDocument";

const UI = join(import.meta.dirname, "..");
const fixtures = readdirSync(join(UI, "e2e", "fixture"))
  .filter((f) => /^policy.*\.yaml$/.test(f))
  .map((f) => ({ name: `e2e/fixture/${f}`, source: readFileSync(join(UI, "e2e", "fixture", f), "utf8") }));
const corpus = { name: "test/policy-corpus.yaml", source: readFileSync(join(UI, "test", "policy-corpus.yaml"), "utf8") };
const v1 = fixtures.find((f) => f.name.endsWith("/policy.yaml"))?.source ?? "";

function visual(source: string): VisualBundle {
  const v = visualOf(parsePolicy(source));
  if (typeof v === "string") throw new Error(`not visual: ${v}`);
  return v;
}

function edited(source: string, e: Edit | Edit[]): string {
  const a = Array.isArray(e) ? applyEdits(source, e) : applyEdit(parsePolicy(source), e);
  if (a.kind !== "ok") throw new Error(`refused: ${a.reason}`);
  return a.source;
}

/** The lines that differ between two texts, as [before, after] (a line-level LCS). */
function changedLines(a: string, b: string): { removed: string[]; added: string[] } {
  const x = a.split("\n");
  const y = b.split("\n");
  const n = x.length;
  const m = y.length;
  const dp: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--)
    for (let j = m - 1; j >= 0; j--) dp[i]![j] = x[i] === y[j] ? dp[i + 1]![j + 1]! + 1 : Math.max(dp[i + 1]![j]!, dp[i]![j + 1]!);
  const removed: string[] = [];
  const added: string[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (x[i] === y[j]) {
      i++;
      j++;
    } else if (dp[i + 1]![j]! >= dp[i]![j + 1]!) removed.push(x[i++]!);
    else added.push(y[j++]!);
  }
  while (i < n) removed.push(x[i++]!);
  while (j < m) added.push(y[j++]!);
  return { removed, added };
}

function sortedComments(s: string): string[] {
  return commentsOf(s).sort();
}

describe("a round trip is lossless", () => {
  it("covers the fixture and the corpus", () => {
    expect(fixtures.length).toBeGreaterThanOrEqual(3);
  });
  for (const f of [...fixtures, corpus]) {
    it(`${f.name} loads into the visual view and writes back byte-identical`, () => {
      const d = parsePolicy(f.source);
      expect(d.ok).toBe(true);
      const v = visualOf(d);
      expect(typeof v).toBe("object");
      expect(serialise(d)).toBe(f.source);
      // Writing back what was read, after reading it into the view, is the same bytes.
      expect(serialise(parsePolicy(serialise(d)))).toBe(f.source);
    });
  }
  it("holds bytes the view never shows: odd spacing, CRLF, a missing final newline, tabs in comments", () => {
    for (const s of ["a:   1\r\nrules:\r\n  - id: x   #\tc\r\n", "rules: [ ]", "# only a comment", "", "rules:\n- id: a\n  effect: deny"]) {
      expect(serialise(parsePolicy(s))).toBe(s);
    }
  });
});

describe("reading", () => {
  it("lists the rules in the order they are tried, each at its line", () => {
    const v = visual(v1);
    expect(v.rules.map((r) => r.id)).toEqual(["ops-db-shell", "vault-through-dmz", "no-shell-on-build-hosts", "build-through-hops"].slice(0, v.rules.length));
    const lines = v1.split("\n");
    for (const r of v.rules) expect(lines[r.line - 1]).toContain(`- id: ${r.id}`);
    expect(v.structural).toBe(true);
    expect(v.groups).toEqual(["sre"]);
    expect(v.labels).toEqual([
      { key: "env", values: ["prod", "dev"] },
      { key: "kind", values: ["db", "vault", "build"] },
    ]);
  });
  it("reads a rule's match as terms by axis, and its route", () => {
    const r = visual(v1).rules[0]!;
    expect(r.effect).toBe("allow");
    expect(r.match).toEqual([
      { axis: "subject", term: "groups", values: ["sre"], modelled: true },
      { axis: "target", term: "labels.env", values: ["prod"], modelled: true },
      { axis: "target", term: "labels.kind", values: ["db"], modelled: true },
    ]);
    expect(r.route).toMatchObject({ intent: "hops-permitted", channels: ["session"], requestTypes: ["pty-req", "shell", "exec"], filterMode: "blacklist" });
    expect(r.route?.credentials[0]).toMatchObject({ method: "ephemeral-user", usernameSource: "subject-local-part" });
    expect(r.obligations).toEqual(["record-session"]);
  });
  it("lists what it does not model, at its line, rather than dropping it", () => {
    const v = visual(corpus.source);
    expect(v.unmodelled.map((u) => u.path)).toEqual(["algorithm_bans"]);
    const paths = v.rules.flatMap((r) => r.unmodelled.map((u) => u.path));
    for (const p of ["match.subject.claims", "match.device.required", "match.device.posture", "match.context.time_of_day", "route.port", "route.forwards", "route.global_requests", "route.max_session_duration", "route.concurrency", "route.algorithm_bans", "route.filter.rules", "route.filter.restricted_exec", "route.requests.subsystems", "route.enforcement.attestation", "obligations.require-approval.approver_groups"]) {
      expect(paths).toContain(p);
    }
    const lines = corpus.source.split("\n");
    for (const r of v.rules) for (const u of r.unmodelled) expect(lines[u.line - 1]?.trim().length).toBeGreaterThan(0);
  });
  it("places a note at a line in the rule whose lines hold it", () => {
    const v = visual(v1);
    const r = v.rules[1]!;
    expect(ruleAtLine(v.rules, r.line)?.id).toBe(r.id);
    expect(ruleAtLine(v.rules, r.endLine)?.id).toBe(r.id);
    expect(ruleAtLine(v.rules, 1)).toBeUndefined();
  });
  it("cannot show a source that is not a mapping, or not YAML", () => {
    expect(visualOf(parsePolicy("rules: [a\n"))).toBe("not_yaml");
    expect(visualOf(parsePolicy("- a\n- b\n"))).toBe("not_mapping");
    // A stream of several documents is not one bundle.
    expect(visualOf(parsePolicy("a: 1\n---\nb: 2\n"))).toBe("not_yaml");
  });
});

describe("an edit changes only what it says", () => {
  it("replaces a list where it stands", () => {
    const after = edited(v1, { op: "set", path: ["rules", 0, "match", "subject", "groups"], value: ["sre", "dba"] });
    const d = changedLines(v1, after);
    expect(d.removed).toEqual(["        groups: [sre]"]);
    expect(d.added).toEqual(["        groups: [sre, dba]"]);
  });
  it("adds a missing key after its last sibling, nesting what is missing", () => {
    const rule = visual(v1).rules.findIndex((r) => r.match.every((m) => m.axis !== "subject"));
    expect(rule).toBeGreaterThan(-1);
    const after = edited(v1, { op: "set", path: ["rules", rule, "match", "subject", "groups"], value: ["sre"] });
    const d = changedLines(v1, after);
    expect(d.removed).toEqual([]);
    expect(d.added).toEqual(["      subject:", "        groups: [sre]"]);
    expect(visual(after).rules[rule]?.match).toContainEqual({ axis: "subject", term: "groups", values: ["sre"], modelled: true });
  });
  it("sets a key in a flow map, and a label inside one", () => {
    const after = edited(v1, { op: "set", path: ["rules", 0, "match", "target", "labels", "env"], value: ["prod", "dev"] });
    expect(changedLines(v1, after)).toEqual({ removed: ["        labels: {env: [prod], kind: [db]}"], added: ["        labels: {env: [prod, dev], kind: [db]}"] });
    const removed = edited(v1, { op: "set", path: ["rules", 0, "match", "target", "labels", "kind"], value: [] });
    expect(changedLines(v1, removed)).toEqual({ removed: ["        labels: {env: [prod], kind: [db]}"], added: ["        labels: {env: [prod]}"] });
  });
  it("removes a key the author empties, and the map it leaves empty, but never the rule", () => {
    const after = edited(v1, { op: "set", path: ["rules", 0, "match", "subject", "groups"], value: [] });
    expect(changedLines(v1, after)).toEqual({ removed: ["      subject:", "        groups: [sre]"], added: [] });
    const r0 = visual(v1).rules[0]!;
    let s = v1;
    for (const m of r0.match) s = edited(s, { op: "set", path: ["rules", 0, "match", m.axis, ...m.term.split(".")], value: [] });
    expect(visual(s).rules[0]?.id).toBe("ops-db-shell");
    expect(s).not.toContain("    match:\n      route");
  });
  it("replaces a scalar, keeping how it was quoted", () => {
    const after = edited(corpus.source, { op: "set", path: ["description"], value: "a new description" });
    expect(changedLines(corpus.source, after).added).toEqual(['description: "a new description"   # quoted on purpose']);
    const plain = edited(v1, { op: "set", path: ["rules", 0, "description"], value: "a shell: recorded" });
    expect(changedLines(v1, plain).added).toEqual(['    description: "a shell: recorded"']);
  });
  it("quotes a value YAML would read as something else, so a string stays a string", () => {
    const after = edited(v1, { op: "set", path: ["rules", 0, "match", "target", "hostnames"], value: ["*.db.example.internal", "true", "db-01"] });
    expect(after).toContain('hostnames: ["*.db.example.internal", "true", db-01]');
    expect(visual(after).rules[0]?.match.find((m) => m.term === "hostnames")?.values).toEqual(["*.db.example.internal", "true", "db-01"]);
  });
  it("edits a block list item by item, keeping the comment beside an item it kept", () => {
    const after = edited(corpus.source, { op: "set", path: ["labels", "kind"], value: ["db", "vault", "appliance"] });
    const d = changedLines(corpus.source, after);
    expect(d.removed).toEqual(["    - fw"]);
    expect(d.added).toEqual(["    - appliance"]);
    expect(after).toContain("    - vault   # a block list, beside the flow one above");
  });
  it("keeps a comment beside a key it removes, as a line of its own", () => {
    const src = "rules:\n  - id: a\n    effect: deny\n    reason: r\n    match:\n      target:\n        zones: [z]   # the zone\n        hostnames: [h]\n";
    const after = edited(src, { op: "set", path: ["rules", 0, "match", "target", "zones"], value: [] });
    expect(after).toContain("        # the zone\n        hostnames: [h]");
  });
  it("refuses an edit that would rewrite a comment inside a flow list", () => {
    const src = "groups: [sre, # on call\n  dba]\nrules:\n  - id: a\n    effect: deny\n    reason: r\n";
    const a = applyEdit(parsePolicy(src), { op: "set", path: ["groups"], value: ["sre"] });
    expect(a).toEqual({ kind: "refused", reason: "structure" });
  });
  it("sets an effect, a username and the obligations", () => {
    let s = edited(v1, { op: "set", path: ["rules", 0, "effect"], value: "deny" });
    expect(changedLines(v1, s)).toEqual({ removed: ["    effect: allow"], added: ["    effect: deny"] });
    s = edited(v1, { op: "username", rule: 0, entry: 0, source: "literal", literal: "dba" });
    expect(changedLines(v1, s)).toEqual({ removed: ["          username: {from: subject-local-part}"], added: ["          username: dba"] });
    s = edited(s, { op: "username", rule: 0, entry: 0, source: "subject" });
    expect(changedLines(v1, s)).toEqual({ removed: ["          username: {from: subject-local-part}"], added: ["          username: {from: subject}"] });
    s = edited(v1, { op: "obligations", rule: 0, kinds: ["record-session", "require-step-up"] });
    expect(changedLines(v1, s)).toEqual({ removed: [], added: ["      - kind: require-step-up"] });
    s = edited(v1, { op: "obligations", rule: 0, kinds: [] });
    expect(changedLines(v1, s)).toEqual({ removed: ["    obligations:", "      - kind: record-session"], added: [] });
    s = edited(v1, { op: "obligations", rule: 1, kinds: ["require-approval"] });
    expect(visual(s).rules[1]?.obligations).toEqual(["require-approval"]);
  });
  it("adds and removes a ladder entry", () => {
    const s = edited(v1, { op: "addCredential", rule: 0, method: "brokered-key" });
    expect(changedLines(v1, s)).toEqual({ removed: [], added: ["        - method: brokered-key"] });
    expect(edited(s, { op: "removeCredential", rule: 0, entry: 1 })).toBe(v1);
  });
  it("writes a route into an empty one", () => {
    const src = "rules:\n  - id: a\n    effect: allow\n    route: {}\n";
    const s = edited(src, [
      { op: "set", path: ["rules", 0, "route", "intent"], value: "direct" },
      { op: "set", path: ["rules", 0, "route", "channels"], value: ["session"] },
      { op: "set", path: ["rules", 0, "route", "filter", "mode"], value: "blacklist" },
      { op: "addCredential", rule: 0, method: "ephemeral-user" },
      { op: "username", rule: 0, entry: 0, source: "subject-local-part" },
    ]);
    expect(s).toBe(
      "rules:\n  - id: a\n    effect: allow\n    route:\n      intent: direct\n      channels: [session]\n      filter:\n        mode: blacklist\n      credentials:\n        - method: ephemeral-user\n          username: {from: subject-local-part}\n",
    );
  });
});

describe("rules move, arrive and leave as their own lines", () => {
  it("moves a rule with its comments, keeping the blank lines where they were", () => {
    const after = edited(v1, { op: "move", from: 2, to: 0 });
    const v = visual(after);
    expect(v.rules.map((r) => r.id).slice(0, 3)).toEqual(["no-shell-on-build-hosts", "ops-db-shell", "vault-through-dmz"]);
    expect(sortedComments(after)).toEqual(sortedComments(v1));
    // The comment above a rule travels with it.
    expect(after.indexOf("# A deny by a rule")).toBeLessThan(after.indexOf("- id: no-shell-on-build-hosts"));
    expect(after.indexOf("- id: no-shell-on-build-hosts")).toBeLessThan(after.indexOf("# An SRE's recorded shell"));
    expect(edited(after, { op: "move", from: 0, to: 2 })).toBe(v1);
  });
  it("moves every rule to every place and back, byte-identical", () => {
    for (const f of [...fixtures, corpus]) {
      const n = visual(f.source).rules.length;
      for (let from = 0; from < n; from++)
        for (let to = 0; to < n; to++) {
          const there = edited(f.source, { op: "move", from, to });
          expect(visual(there).rules[to]?.id).toBe(visual(f.source).rules[from]?.id);
          expect(edited(there, { op: "move", from: to, to: from })).toBe(f.source);
        }
    }
  });
  it("adds a rule at the end, before the default deny, and removes it again", () => {
    const id = freshRuleId(visual(v1).rules);
    const after = edited(v1, { op: "addRule", id });
    const v = visual(after);
    expect(v.rules[v.rules.length - 1]).toMatchObject({ id, effect: "deny", reason: "" });
    expect(changedLines(v1, after).removed).toEqual([]);
    expect(edited(after, { op: "removeRule", index: v.rules.length - 1 })).toBe(v1);
  });
  it("removes a rule with the comments written with it, and only those", () => {
    const after = edited(v1, { op: "removeRule", index: 0 });
    expect(visual(after).rules.map((r) => r.id)).not.toContain("ops-db-shell");
    expect(after).not.toContain("# An SRE's recorded shell");
    expect(after).toContain("# The vault sits in the DMZ");
  });
  it("names a rule id nobody uses", () => {
    expect(freshRuleId([{ id: "rule-2" }, { id: "x" }])).toBe("rule-3");
    expect(freshRuleId([{ id: "rule-1" }])).toBe("rule-2");
  });
});

describe("free text is checked where the server checks it, with its codes", () => {
  // What Go's netip answers for each (compile.parseNetwork: ParsePrefix, then
  // ParseAddr), taken by running it; a zone swallows whatever follows `%`.
  const NETIP: [string, boolean][] = [
    ["10.40.0.0/16", true], ["10.0.0.1", true], ["10.0.0.256", false], ["10.0.0.01", false],
    ["10.0.0.0/33", false], ["10.0.0.0/08", false], ["fd00::/8", true], ["::1", true], ["::", true],
    ["1::2::3", false], ["fe80::1%eth0", true], ["fe80::1%eth0/64", true], ["::ffff:10.0.0.1", true],
    ["1:2:3:4:5:6:7:8", true], ["1:2:3:4:5:6:7:8:9", false], ["1:2:3:4:5:6:7::", true],
    ["1:2:3:4:5:6:7:8::", false], ["gggg::", false], ["10.0.0.0/", false], ["/8", false], ["", false],
    ["10.0.0", false], ["fe80::1%", false], ["::/129", false], ["12345::", false],
  ];
  it.each(NETIP)("a network %j is %s", (s, ok) => {
    expect(checkCidr(s)).toEqual(ok ? null : { code: "match.invalid_cidr", parameters: { cidr: s } });
  });
  it("checks a hostname pattern as model.ValidHostPattern does", () => {
    for (const ok of ["db-01.example.internal", "*.example.internal"]) expect(checkHostPattern(ok)).toBeNull();
    for (const bad of ["", "*", "*.", "db*.example", "*.*.example"]) expect(checkHostPattern(bad)?.code).toBe("match.invalid_hostname_pattern");
  });
  it("checks an id is there and used once, naming the other's line", () => {
    const v = visual(v1);
    expect(checkRuleId(" ", v.rules, 0)).toEqual({ code: "rule.id_missing" });
    expect(checkRuleId("vault-through-dmz", v.rules, 0)).toEqual({
      code: "rule.id_duplicate",
      parameters: { id: "vault-through-dmz", other_line: String(v.rules[1]!.line) },
    });
    expect(checkRuleId("ops-db-shell", v.rules, 0)).toBeNull();
    expect(checkReason("")).toEqual({ code: "rule.deny_needs_reason" });
  });
});
