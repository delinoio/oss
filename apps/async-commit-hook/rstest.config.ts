import { pluginReact } from "@rsbuild/plugin-react";
import { defineConfig } from "@rstest/core";
export default defineConfig({
  plugins: [pluginReact()],
  include: ["src/**/*.test.tsx", "src/**/*.test.ts"],
  testEnvironment: "jsdom",
  // CI runs these jsdom tests alongside Go compilation and UI builds. Keep one
  // worker so navigation reads meet their existing deadlines under that load.
  // Increase only after validating the complete concurrent CI task graph.
  pool: { maxWorkers: 1 },
  setupFiles: ["./src/test-setup.ts"],
});
