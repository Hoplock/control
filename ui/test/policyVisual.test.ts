// THE VISUAL POLICY EDITOR'S RULES (0066; PLAN M3, §5.3).
//   1. Every enumerated choice is the server's. The vocabulary file the
//      console builds from is generated from internal/policy/model's kinds
//      and held equal to them by TestTheConsolesPolicyVocabularyIsTheServers;
//      this holds the other half: no option list in the editor is written by
//      hand. Read from the syntax tree, no array literal in the editor's
//      source names a member of a closed vocabulary, every `options` the
//      editor hands a control is the vocabulary's or the bundle's own
//      declarations, and no `<option>` carries a literal value but the empty
//      one ("not set", "add…").
//   2. A note sits on the card of the rule it names, by id or by line, and
//      the rest at document scope; a trace marks its rule and its terms, by
//      the names the engine gives them.

import { readFileSync } from "node:fs";
import { join } from "node:path";
import ts from "typescript";
import { describe, expect, it } from "vitest";
import type { Finding, Rejection } from "../src/api/types";
import { parsePolicy, visualOf, type VisualBundle } from "../src/screens/policyDocument";
import { chipsByAxis, effectStatus, notesByRule, routeSummary, traceMark } from "../src/screens/policyVisualModel";
import data from "../src/screens/policyVocabulary.json";
import { MATCH_AXES, vocabulary } from "../src/screens/policyVocabulary";
import { src } from "./source";

const EDITOR = ["screens/PolicyVisual.tsx", "screens/policyVisualModel.ts", "screens/policyDocument.ts", "components/SortableList.tsx", "components/ChipEditor.tsx"];
const VOCABULARIES: readonly (readonly string[])[] = Object.values(data.vocabularies);

/** An array that lists two or more members of one vocabulary and nothing else: an option list. */
function isOptionList(l: ts.ArrayLiteralExpression): boolean {
  const texts = l.elements.map((e) => (ts.isStringLiteral(e) ? e.text : null));
  if (texts.length < 2 || texts.some((t) => t === null)) return false;
  return VOCABULARIES.some((members) => texts.every((t) => members.includes(t as string)));
}

function tree(file: string): ts.SourceFile {
  return ts.createSourceFile(file, readFileSync(join(src, file), "utf8"), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
}

function nodes(sf: ts.SourceFile, keep: (n: ts.Node) => boolean): ts.Node[] {
  const out: ts.Node[] = [];
  const visit = (n: ts.Node) => {
    if (keep(n)) out.push(n);
    ts.forEachChild(n, visit);
  };
  visit(sf);
  return out;
}

function where(sf: ts.SourceFile, n: ts.Node): string {
  return `${sf.fileName}:${sf.getLineAndCharacterOfPosition(n.getStart()).line + 1}`;
}

describe("every enumerated choice is the server's", () => {
  it("knows an option list when it sees one", () => {
    const list = (text: string) => (ts.createSourceFile("x.ts", `const x = ${text};`, ts.ScriptTarget.Latest, true).statements[0] as ts.VariableStatement).declarationList.declarations[0]?.initializer as ts.ArrayLiteralExpression;
    expect(isOptionList(list('["allow", "deny"]'))).toBe(true);
    expect(isOptionList(list('["session", "x11"]'))).toBe(true);
    expect(isOptionList(list('["route", "target"]'))).toBe(false);
    expect(isOptionList(list('["session", v]'))).toBe(false);
  });

  it("reads the generated vocabulary, which names every closed kind", () => {
    expect(Object.keys(data.vocabularies).length).toBeGreaterThanOrEqual(20);
    for (const name of ["effect", "route_intent", "channel_type", "request_type", "filter_mode", "credential_method", "execution_rung", "reach_rung", "obligation_kind", "cache_key_component"] as const) {
      expect(vocabulary(name).length).toBeGreaterThan(0);
    }
    expect(MATCH_AXES).toEqual(["subject", "device", "context", "target", "grant"]);
  });

  it.each(EDITOR)("%s writes no array of a vocabulary's members by hand", (file) => {
    const sf = tree(file);
    const lists = nodes(sf, (n) => ts.isArrayLiteralExpression(n)) as ts.ArrayLiteralExpression[];
    const typed = lists.filter(isOptionList);
    expect(typed.map((l) => `${where(sf, l)} ${l.getText()}`)).toEqual([]);
  });

  it("hands every control its options from the vocabulary or the bundle's own declarations", () => {
    const sf = tree("screens/PolicyVisual.tsx");
    const attrs = nodes(sf, (n) => ts.isJsxAttribute(n) && n.name.getText() === "options") as ts.JsxAttribute[];
    expect(attrs.length).toBeGreaterThan(10);
    const allowed = /^(vocabulary\("[a-z_]+"\)|visual\.(groups|scopes)|l\.values|opts\.options)$/;
    const bad = attrs
      .map((a) => (a.initializer && ts.isJsxExpression(a.initializer) ? a.initializer.expression?.getText() ?? "" : a.getText()))
      .filter((x) => !allowed.test(x));
    expect(bad).toEqual([]);
  });

  it("writes no <option> with a literal value but the empty one", () => {
    for (const file of ["screens/PolicyVisual.tsx", "components/ChipEditor.tsx"]) {
      const sf = tree(file);
      const options = nodes(sf, (n) => (ts.isJsxOpeningElement(n) || ts.isJsxSelfClosingElement(n)) && n.tagName.getText() === "option") as (
        | ts.JsxOpeningElement
        | ts.JsxSelfClosingElement
      )[];
      const literal = options.filter((o) =>
        o.attributes.properties.some(
          (p) => ts.isJsxAttribute(p) && p.name.getText() === "value" && p.initializer !== undefined && ts.isStringLiteral(p.initializer) && p.initializer.text !== "",
        ),
      );
      expect(literal.map((o) => where(sf, o))).toEqual([]);
    }
  });
});

const V1 = readFileSync(join(src, "..", "e2e", "fixture", "policy.yaml"), "utf8");

function visual(source: string): VisualBundle {
  const v = visualOf(parsePolicy(source));
  if (typeof v === "string") throw new Error(v);
  return v;
}

const rejection = (code: string, rule?: string, line?: number): Rejection => ({ code, rule, line, message: "" });
const finding = (id: string, rule?: string, line?: number): Finding => ({ id, code: id, severity: "warning", source: "control", rule, line, message: "" });

describe("a note sits on its rule's card", () => {
  const v = visual(V1);
  const [r0, r1] = v.rules;
  it("by the rule it names, or by its line, and the rest at document scope", () => {
    const { byRule, document } = notesByRule(
      v.rules,
      [rejection("match.unknown_group", r0?.id, r0?.line), rejection("rule.unreachable", undefined, r1?.endLine), rejection("policy.no_rules")],
      [finding("authoring.x/one", r1?.id), finding("satisfiability.y/")],
    );
    expect(byRule.get(0)?.rejections.map((r) => r.code)).toEqual(["match.unknown_group"]);
    expect(byRule.get(1)?.rejections.map((r) => r.code)).toEqual(["rule.unreachable"]);
    expect(byRule.get(1)?.findings.map((f) => f.id)).toEqual(["authoring.x/one"]);
    expect(document.rejections.map((r) => r.code)).toEqual(["policy.no_rules"]);
    expect(document.findings.map((f) => f.id)).toEqual(["satisfiability.y/"]);
  });
  it("tells two rules with one id apart by the line", () => {
    const twice = V1.replace("- id: vault-through-dmz", "- id: ops-db-shell");
    const w = visual(twice);
    const second = w.rules[1];
    const { byRule } = notesByRule(w.rules, [rejection("rule.id_duplicate", "ops-db-shell", second?.line)], []);
    expect(byRule.get(1)?.rejections).toHaveLength(1);
    expect(byRule.get(0)).toBeUndefined();
  });
});

describe("a rule reads as chips, and a trace marks them by the engine's names", () => {
  it("names each chip as the engine names a matched term", () => {
    const corpus = visual(readFileSync(join(src, "..", "test", "policy-corpus.yaml"), "utf8"));
    const chips = chipsByAxis(corpus.rules[0]?.match ?? []).flatMap((a) => a.chips.map((c) => c.key));
    for (const k of ["subject.groups=dba", "subject.id=alice", "subject.source=corp-oidc", "subject.auth_method=cert", "context.day=Monday", "context.source_network=10.40.0.0/16", "context.proxy_id=px-core-01", "target.hostname=*.db.example.internal", "target.labels.env=prod", "target.zone=core"]) {
      expect(chips).toContain(k);
    }
    expect(chipsByAxis(corpus.rules[0]?.match ?? []).map((a) => a.axis)).toEqual(["subject", "device", "context", "target"]);
  });
  it("marks the rule that answered, or the default deny", () => {
    const v = visual(V1);
    expect(traceMark(v.rules, { effect: "allow", rule: "vault-through-dmz", rule_line: v.rules[1]?.line, terms: ["subject.groups=sre"] })).toEqual({
      rule: 1,
      matched: new Set(["subject.groups=sre"]),
    });
    expect(traceMark(v.rules, { effect: "deny" }).rule).toBe("default");
  });
  it("says a route in one line and an effect in the status vocabulary", () => {
    const v = visual(V1);
    expect(routeSummary(v.rules[0] as never)).toEqual(["hops-permitted", "session", "pty-req, shell, exec", "ephemeral-user"]);
    expect([effectStatus("allow"), effectStatus("deny"), effectStatus(undefined), effectStatus("maybe")]).toEqual(["allowed", "denied", "notReported", "notReported"]);
  });
});
