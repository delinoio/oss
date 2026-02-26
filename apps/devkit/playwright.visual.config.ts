import { defineConfig, devices } from "@playwright/test";

const visualQaBaseUrl = process.env.VISUAL_QA_BASE_URL ?? "http://127.0.0.1:3100";
const shouldSkipWebServer = process.env.VISUAL_QA_SKIP_WEBSERVER === "1";

export default defineConfig({
  testDir: "./e2e",
  testMatch: /visual-qa\.spec\.ts/,
  timeout: 180_000,
  expect: {
    timeout: 30_000,
  },
  fullyParallel: false,
  retries: process.env.CI ? 1 : 0,
  reporter: [
    ["list"],
    ["html", { outputFolder: "playwright-report/visual-qa", open: "never" }],
    ["json", { outputFile: "playwright-report/visual-qa/results.json" }],
  ],
  outputDir: "test-results/visual-qa",
  use: {
    baseURL: visualQaBaseUrl,
    headless: true,
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
    video: "retain-on-failure",
    viewport: {
      width: 1440,
      height: 900,
    },
  },
  webServer: shouldSkipWebServer
    ? undefined
    : {
        command: "pnpm dev --port 3100",
        url: visualQaBaseUrl,
        cwd: __dirname,
        timeout: 180_000,
        reuseExistingServer: !process.env.CI,
      },
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
      },
    },
  ],
});
