// The design-direction screenshots (0052): a TOOL, not a check. It asserts
// nothing and is never a CI job; its files are `*.shoot.ts`, outside the
// `*.spec.ts` that every console-e2e job runs. It shares the browser checks'
// server, fixture and launch flags, so an image is of the screen the
// baseline holds, with a candidate token file applied in the browser.
//
//   DESIGN_ROUND=1 npx playwright test -c playwright.design.config.ts
//
// writes design/round-1/<id>-<k>-<screen>-<theme>.png for every
// design/round-1/<id>.css (see e2e/design-directions.shoot.ts).

import { defineConfig } from "@playwright/test";
import base from "./playwright.config";

export default defineConfig({
  testDir: "e2e",
  testMatch: /\.shoot\.ts$/,
  outputDir: "build/design/results",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: "list",
  globalSetup: "./e2e/global-setup.ts",
  timeout: 600_000,
  use: base.use,
  webServer: base.webServer,
});
