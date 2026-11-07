/// <reference types="vitest/config" />
// The console's build (ui/DESIGN.md §11, PLAN M20).
//
// `vite build` writes the production console into internal/console/dist,
// which is committed and embedded in the server binary, so `make build`
// needs no Node; CI rebuilds it and fails on any difference (the drift
// check). `vite build --mode en-XA | en-XB` writes a pseudolocale build into
// build/<mode>, which is never committed: en.json is the only catalogue in
// the tree (PLAN M21), and a pseudolocale is a build of it, not a file.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

const PSEUDO = ["en-XA", "en-XB"];

/** Serves the catalogue at /console/locales/en.json, as its source has it. */
function catalogue(): Plugin {
  const source = fileURLToPath(new URL("./src/locales/en.json", import.meta.url));
  return {
    name: "hoplock-catalogue",
    apply: "build",
    generateBundle() {
      this.emitFile({ type: "asset", fileName: "locales/en.json", source: readFileSync(source) });
    },
  };
}

export default defineConfig(({ mode }) => {
  const pseudo = PSEUDO.includes(mode);
  return {
    base: "/console/",
    plugins: [react(), catalogue()],
    build: {
      outDir: pseudo ? `build/${mode}` : "../internal/console/dist",
      emptyOutDir: true,
      assetsDir: "assets",
      sourcemap: false,
      target: "es2022",
      reportCompressedSize: false,
      // Fonts are files, never inlined into CSS as data: URIs, so the budget
      // counts them as fonts and the binary carries each once.
      assetsInlineLimit: 0,
    },
    test: {
      include: ["test/**/*.test.{ts,tsx}"],
      environment: "node",
    },
  };
});
