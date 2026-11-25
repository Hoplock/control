// The visual claim-mapping editor checks and offers only what the server and
// the tenant say (0068): no pattern, reserved name or option list is written
// by hand in its source. The rules come from claimMappingRules.json, which a Go
// test generates from internal/identity; a directory is chosen from the
// tenant's sources and a group offered from its groups, both read from the API.

import { readFileSync } from "node:fs";
import { join } from "node:path";
import ts from "typescript";
import { describe, expect, it } from "vitest";
import rules from "../src/screens/claimMappingRules.json";
import { src } from "./source";

const EDITOR = [
  "screens/ClaimMapping.tsx",
  "screens/ClaimMappingVisual.tsx",
  "screens/ClaimMappingPreview.tsx",
  "screens/claimMappingDocument.ts",
  "components/ChipEditor.tsx",
  "components/Field.tsx",
];

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

/** Every name the server reserves, and every pattern it checks, as they would be written by hand. */
const SERVERS = [...rules.reserved.names, rules.reserved.prefix, ...Object.values(rules.patterns)];

describe("every rule the editor checks is the server's", () => {
  it.each(EDITOR)("%s writes no reserved name or pattern of the server's by hand", (file) => {
    const sf = tree(file);
    const literals = nodes(sf, (n) => ts.isStringLiteral(n) || ts.isNoSubstitutionTemplateLiteral(n) || ts.isRegularExpressionLiteral(n));
    const copied = literals.filter((n) => {
      const text = ts.isRegularExpressionLiteral(n) ? n.text.slice(1, n.text.lastIndexOf("/")) : (n as ts.StringLiteral).text;
      return SERVERS.some((s) => text === s || (s.length > 3 && text.includes(s)));
    });
    expect(copied.map((n) => `${where(sf, n)} ${n.getText()}`)).toEqual([]);
  });

  it("knows a copied rule when it sees one", () => {
    const sf = ts.createSourceFile("x.ts", 'const a = "chain_hop_proxy_id"; const b = /^[a-z0-9][a-z0-9-]{0,62}$/;', ts.ScriptTarget.Latest, true);
    const literals = nodes(sf, (n) => ts.isStringLiteral(n) || ts.isRegularExpressionLiteral(n));
    const copied = literals.filter((n) => {
      const text = ts.isRegularExpressionLiteral(n) ? n.text.slice(1, n.text.lastIndexOf("/")) : (n as ts.StringLiteral).text;
      return SERVERS.some((s) => text === s || (s.length > 3 && text.includes(s)));
    });
    expect(copied).toHaveLength(2);
  });

  it("builds every check it makes from the generated rules", () => {
    const text = readFileSync(join(src, "screens/claimMappingDocument.ts"), "utf8");
    expect(text).toContain('import rules from "./claimMappingRules.json"');
    // The only regular expressions it builds are the generated patterns.
    const sf = tree("screens/claimMappingDocument.ts");
    const built = nodes(sf, (n) => ts.isNewExpression(n) && n.expression.getText() === "RegExp") as ts.NewExpression[];
    expect(built.map((n) => n.arguments?.[0]?.getText())).toEqual(["p"]);
  });
});

describe("every option the editor offers is the tenant's", () => {
  it("hands every chip editor and field its options from what the API answered", () => {
    const allowed = /^(options|offered\.sources|offered\.groups)$/;
    const bad: string[] = [];
    let seen = 0;
    for (const file of ["screens/ClaimMappingVisual.tsx", "screens/ClaimMappingPreview.tsx"]) {
      const sf = tree(file);
      const attrs = nodes(sf, (n) => {
        if (!ts.isJsxAttribute(n)) return false;
        const name = n.name.getText();
        const tag = (n.parent.parent as ts.JsxOpeningLikeElement).tagName.getText();
        return name === "options" || name === "suggestions" || (name === "values" && tag === "Suggestions");
      }) as ts.JsxAttribute[];
      for (const a of attrs) {
        seen++;
        const expr = a.initializer && ts.isJsxExpression(a.initializer) ? (a.initializer.expression?.getText() ?? "") : a.getText();
        if (!allowed.test(expr)) bad.push(`${where(sf, a)} ${a.getText()}`);
      }
    }
    expect(seen).toBeGreaterThanOrEqual(5);
    expect(bad).toEqual([]);
  });

  it("writes no <option> with a literal value but the empty one and the preview's own door", () => {
    for (const file of ["screens/ClaimMappingVisual.tsx", "screens/ClaimMappingPreview.tsx"]) {
      const sf = tree(file);
      const options = nodes(sf, (n) => (ts.isJsxOpeningElement(n) || ts.isJsxSelfClosingElement(n)) && n.tagName.getText() === "option") as ts.JsxOpeningLikeElement[];
      const literal = options.filter((o) =>
        o.attributes.properties.some(
          (p) => ts.isJsxAttribute(p) && p.name.getText() === "value" && p.initializer !== undefined && ts.isStringLiteral(p.initializer) && !["", "signIn"].includes(p.initializer.text),
        ),
      );
      expect(literal.map((o) => `${where(sf, o)} ${o.getText()}`)).toEqual([]);
    }
  });
});
