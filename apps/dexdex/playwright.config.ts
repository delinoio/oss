import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  expect: {
    toHaveScreenshot: {
      maxDiffPixelRatio: 0.005,
      animations: "disabled",
    },
  },
  fullyParallel: false,
  reporter: [["list"]],
  use: {
    baseURL: "http://127.0.0.1:5991",
    viewport: { width: 1440, height: 900 },
    trace: "on-first-retry",
    ...devices["Desktop Chrome"],
  },
  webServer: {
    command: "pnpm --filter dexdex dev --host 127.0.0.1",
    url: "http://127.0.0.1:5991",
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
