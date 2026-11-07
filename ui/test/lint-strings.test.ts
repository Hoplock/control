// The string lint, shown failing on each thing it exists to catch.

import { describe, expect, it } from "vitest";
// @ts-expect-error -- a plain .mjs script, with no declarations.
import { lintSource } from "../scripts/lint-strings.mjs";

const lint = (text: string, file = "src/x/C.tsx"): string[] => lintSource(file, text);

describe("the string and format lint (DESIGN.md §10.9)", () => {
  it.each([
    ["JSX text", `export const C = () => <p>Hello</p>;`],
    ["a string child", `export const C = () => <p>{"Hello"}</p>;`],
    ["an aria-label", `export const C = () => <button aria-label="Close" />;`],
    ["a title in braces", `export const C = () => <span title={"Version"} />;`],
    ["a placeholder", `export const C = () => <input placeholder="Search" />;`],
    ["Intl outside the helpers", `export const x = new Intl.NumberFormat("en").format(1);`],
    ["toLocaleString", `export const x = (1).toLocaleString();`],
    ["toISOString", `export const x = new Date().toISOString();`],
    ["an inline style", `export const C = () => <p style={{}} />;`],
    ["a raw colour", `export const c = "#ff0000";`],
  ])("fails on %s", (_, text) => {
    expect(lint(text)).not.toEqual([]);
  });

  it("passes catalogue text, identifiers and punctuation", () => {
    expect(lint(`export const C = ({ id, t }) => <p title={id}>{t("k.v")} · {id}</p>;`)).toEqual([]);
  });

  it("lets the locale helpers format", () => {
    expect(lint(`export const x = new Intl.NumberFormat("en");`, "src/i18n/format.ts")).toEqual([]);
  });
});
