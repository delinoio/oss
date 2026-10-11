import { defineConfig } from "vitest/config";
export default defineConfig({
  test: {
    include: ["src/**/*.test.tsx", "src/**/*.test.ts"],
    environment: "jsdom",
    // CI runs these jsdom tests alongside Go compilation and UI builds. Keep one
    // worker so navigation reads meet their existing deadlines under that load.
    // Increase only after validating the complete concurrent CI task graph.
    maxWorkers: 1,
    setupFiles: ["./src/test-setup.ts"],
  },
});
