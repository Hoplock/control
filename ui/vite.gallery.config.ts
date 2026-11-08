// The component gallery's build (0050): a test page, never the product. It
// shares the console's source and tokens, and is built per locale mode —
// `vite build -c vite.gallery.config.ts [--mode en-XA|en-XB]` — into
// build/gallery[-<mode>], which is never committed.

import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig(({ mode }) => ({
  // The console's own base: its router refuses any other (src/router/paths.ts).
  // The E2E run serves this build at /console/gallery in the browser.
  base: "/console/",
  plugins: [react()],
  build: {
    outDir: mode === "en-XA" || mode === "en-XB" ? `build/gallery-${mode}` : "build/gallery",
    emptyOutDir: true,
    sourcemap: false,
    target: "es2022",
    reportCompressedSize: false,
    assetsInlineLimit: 0,
    rollupOptions: { input: "gallery.html" },
  },
}));
