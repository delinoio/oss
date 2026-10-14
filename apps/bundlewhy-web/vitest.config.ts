import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    include: ["src/**/*.test.tsx", "src/**/*.test.ts"],
    environment: "jsdom",
    maxWorkers: 1,
    setupFiles: ["./src/test-setup.ts"],
  },
});
