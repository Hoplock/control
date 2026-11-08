// The console's browser checks (0050; ui/DESIGN.md §10.3–§10.8).
//
// One Playwright run over a REGISTRY of screens (e2e/screens.ts), against a
// real server on a seeded database (e2e/fixture/serve.sh), so a later phase's
// screen is checked by registering it and by nothing else. `npm run e2e`
// builds the pseudolocale and gallery pages first; `npm run e2e:update`
// rewrites the screenshot baseline after a deliberate design change.
//
// Projects run in order: `checks` (everything that reads), then `mutations`
// (what changes the seeded data), so a screenshot never sees a mutation.

import { defineConfig } from "@playwright/test";

const NORTH = "http://127.0.0.1:18081";

export default defineConfig({
  testDir: "e2e",
  outputDir: "build/e2e/results",
  snapshotPathTemplate: "{testDir}/baseline/{arg}{ext}",
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  retries: 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never", outputFolder: "build/e2e/report" }]] : "list",
  globalSetup: "./e2e/global-setup.ts",
  timeout: 60_000,
  expect: {
    timeout: 10_000,
    toHaveScreenshot: {
      // Antialiasing differs a little between machines; a layout change does not.
      maxDiffPixelRatio: 0.01,
      threshold: 0.2,
      animations: "disabled",
      caret: "hide",
      scale: "css",
    },
  },
  use: {
    baseURL: NORTH,
    viewport: { width: 1280, height: 800 },
    deviceScaleFactor: 1,
    locale: "en-US",
    timezoneId: "UTC",
    trace: "off",
  },
  projects: [
    { name: "checks", testIgnore: /mutations\.spec\.ts/ },
    { name: "mutations", testMatch: /mutations\.spec\.ts/, dependencies: ["checks"] },
  ],
  webServer: {
    command: "sh e2e/fixture/serve.sh",
    url: `${NORTH}/console/`,
    reuseExistingServer: !process.env.CI,
    timeout: 300_000,
    stdout: "ignore",
    stderr: "pipe",
  },
});
