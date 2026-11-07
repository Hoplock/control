#!/usr/bin/env node
// THE STRING AND FORMAT LINT (ui/DESIGN.md §10.9, PLAN M21).
//
// Fails the build on:
//   1. user-visible text written in a component rather than taken from the
//      catalogue: JSX text with a letter in it, a string literal as a JSX
//      child, or a string literal in an attribute a person reads or hears
//      (aria-label, title, placeholder, alt, …);
//   2. a date, time, number or list formatted outside the locale helpers
//      (src/i18n): Intl.*, toLocale*String, toFixed, toISOString and the
//      Date to*String family;
//   3. an inline style — styles are static (DESIGN.md §11), and an inline
//      style is where a raw colour or size escapes the token lint;
//   4. a raw colour in a string, for the same reason.
//
// It reads the TypeScript syntax tree, so a comment or an identifier that
// happens to spell a word is not a finding. Tests are not components.
//
// node scripts/lint-strings.mjs [files...]   (default: every src/**/*.ts{,x})

import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

const root = fileURLToPath(new URL("..", import.meta.url));
const src = join(root, "src");

/** Attributes whose value a person reads or hears. */
const VISIBLE_ATTRIBUTES = new Set([
  "aria-label",
  "aria-description",
  "aria-roledescription",
  "aria-valuetext",
  "aria-placeholder",
  "title",
  "placeholder",
  "alt",
  "label",
]);

/** Formatting that belongs to the locale helpers. */
const FORMAT_METHODS = new Set([
  "toLocaleString",
  "toLocaleDateString",
  "toLocaleTimeString",
  "toFixed",
  "toPrecision",
  "toExponential",
  "toISOString",
  "toUTCString",
  "toDateString",
  "toTimeString",
  "toGMTString",
]);

const LETTER = /\p{L}/u;
const RAW_COLOUR = /#[0-9a-f]{3,8}\b|\b(rgba?|hsla?|hwb|oklch|oklab|lab|lch)\(/i;

function walk(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) out.push(...walk(p));
    else if (/\.tsx?$/.test(name) && !/\.d\.ts$/.test(name)) out.push(p);
  }
  return out;
}

function insideI18n(file) {
  return relative(src, file).split(sep)[0] === "i18n";
}

function attributeName(node) {
  return ts.isIdentifier(node.name) ? node.name.text : node.name.getText();
}

/** Lints one file's text; returns its findings. Exported for the lint's own test. */
export function lintSource(file, text) {
  const findings = [];
  const sf = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, file.endsWith("x") ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
  const at = (node, msg) => {
    const { line, character } = sf.getLineAndCharacterOfPosition(node.getStart(sf));
    findings.push(`${relative(root, file)}:${line + 1}:${character + 1}: ${msg}`);
  };
  const i18n = insideI18n(file);

  const visit = (node) => {
    if (ts.isJsxText(node) && LETTER.test(node.text)) {
      at(node, `text ${JSON.stringify(node.text.trim())} in a component; take it from the catalogue`);
    }
    if (ts.isJsxExpression(node) && node.expression && ts.isJsxElement(node.parent)) {
      const e = node.expression;
      if ((ts.isStringLiteral(e) || ts.isNoSubstitutionTemplateLiteral(e)) && LETTER.test(e.text)) {
        at(e, `string ${JSON.stringify(e.text)} rendered as text; take it from the catalogue`);
      }
    }
    if (ts.isJsxAttribute(node)) {
      const name = attributeName(node);
      if (name === "style") at(node, "an inline style; styles are static CSS from the tokens");
      const init = node.initializer;
      if (init && VISIBLE_ATTRIBUTES.has(name)) {
        const lit = ts.isStringLiteral(init)
          ? init
          : ts.isJsxExpression(init) && init.expression && (ts.isStringLiteral(init.expression) || ts.isNoSubstitutionTemplateLiteral(init.expression))
            ? init.expression
            : null;
        if (lit && LETTER.test(lit.text)) at(init, `${name}=${JSON.stringify(lit.text)} is read by a person; take it from the catalogue`);
      }
    }
    if ((ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) && RAW_COLOUR.test(node.text)) {
      at(node, `a raw colour ${JSON.stringify(node.text)}; colours are tokens in tokens.css`);
    }
    if (!i18n) {
      if (ts.isPropertyAccessExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === "Intl") {
        at(node, `Intl.${node.name.text} outside src/i18n; format through the locale helpers`);
      }
      if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) && FORMAT_METHODS.has(node.expression.name.text)) {
        at(node, `${node.expression.name.text}() outside src/i18n; format through the locale helpers`);
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return findings;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  const files = process.argv.length > 2 ? process.argv.slice(2) : walk(src);
  const findings = files.flatMap((f) => lintSource(f, readFileSync(f, "utf8")));
  for (const f of findings) console.error(f);
  if (findings.length > 0) {
    console.error(`lint-strings: ${findings.length} finding(s) in ${files.length} file(s)`);
    process.exit(1);
  }
  console.log(`lint-strings: ${files.length} file(s) OK`);
}
