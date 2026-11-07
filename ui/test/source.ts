// Reads the console's source as a syntax tree, for the tests that hold a
// property of all of it.

import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

export const root = fileURLToPath(new URL("..", import.meta.url));
export const src = join(root, "src");

export function walk(dir: string, match: RegExp): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    return statSync(p).isDirectory() ? walk(p, match) : match.test(name) ? [p] : [];
  });
}

/** Every string literal in the console's TypeScript, with where it is. */
export function stringLiterals(): { file: string; text: string }[] {
  const out: { file: string; text: string }[] = [];
  for (const file of walk(src, /\.tsx?$/)) {
    const sf = ts.createSourceFile(file, readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true);
    const visit = (node: ts.Node) => {
      if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) out.push({ file, text: node.text });
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return out;
}
