// The catalogue key test (PLAN M21, M27; DESIGN.md §9): en.json is the only
// catalogue in the tree, every key it holds is referenced, every key the
// source references exists, every message is ICU that parses, and no key of
// Control's carries the ":" a host's keys are namespaced with.

import { readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { parse } from "@formatjs/icu-messageformat-parser";
import { describe, expect, it } from "vitest";
import en from "../src/locales/en.json";
import { root, src, stringLiterals, walk } from "./source";

const keys = Object.keys(en);
const namespaces = new Set(keys.map((k) => k.split(".")[0]));
const KEY_SHAPE = /^[a-z][a-zA-Z]*(\.[a-zA-Z_]+)+$/;

describe("the catalogue", () => {
  it("is en.json, alone", () => {
    const catalogues = walk(root, /\.json$/)
      .filter((f) => !f.includes("node_modules") && !f.includes(`${join("ui", "build")}`))
      .filter((f) => relative(root, f).split("/").includes("locales"));
    expect(catalogues.map((f) => relative(root, f))).toEqual(["src/locales/en.json"]);
    // The dist copy is generated from it, byte for byte.
    const dist = readFileSync(join(root, "..", "internal", "console", "dist", "locales", "en.json"), "utf8");
    expect(dist).toBe(readFileSync(join(src, "locales", "en.json"), "utf8"));
  });

  it("references every key it holds", () => {
    const used = new Set(stringLiterals().map((l) => l.text));
    const unused = keys.filter((k) => !used.has(k));
    expect(unused, "keys nothing in src/ names").toEqual([]);
  });

  it("holds every key the source references", () => {
    const missing = stringLiterals()
      .filter((l) => KEY_SHAPE.test(l.text) && namespaces.has(l.text.split(".")[0] as string))
      .filter((l) => !(l.text in en))
      .map((l) => `${relative(root, l.file)}: ${l.text}`);
    expect(missing).toEqual([]);
  });

  it("carries no key a host's could be: no ':' in any of Control's", () => {
    expect(keys.filter((k) => k.includes(":"))).toEqual([]);
  });

  it.each(keys)("%s is ICU MessageFormat that parses", (key) => {
    expect(() => parse((en as Record<string, string>)[key] as string)).not.toThrow();
  });
});
