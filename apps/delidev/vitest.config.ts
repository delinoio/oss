import { defineConfig } from "vitest/config";
// Full-shell fixtures load both bundled catalogs in isolated workers. Bound the
// pool so aggregate CPU/memory contention does not consume individual test deadlines.
export default defineConfig({ test: { include: ["src/**/*.test.ts", "src/**/*.test.tsx"], environment: "jsdom", environmentOptions: { jsdom: { url: "http://localhost/" } }, restoreMocks: true, maxWorkers: 4, setupFiles: ["./src/test-setup.ts"] } });
