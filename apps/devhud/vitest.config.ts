import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "jsdom",
    include: [
      "src/**/*.test.{ts,tsx}",
      "realqa-extension/src/**/*.test.js",
    ],
    setupFiles: ["./src/test/setup.ts"],
  },
});
