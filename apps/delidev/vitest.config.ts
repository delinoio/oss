import { defineConfig } from "vitest/config";
// Bound jsdom processes during concurrent native builds so focus/lifetime timers
// remain responsive without extending test or product deadlines. Increase this
// limit only after those suites pass under the shared CI host's peak build load.
export default defineConfig({ test: { include: ["src/**/*.test.ts", "src/**/*.test.tsx"], environment: "jsdom", environmentOptions: { jsdom: { url: "http://localhost/" } }, maxWorkers: 4, restoreMocks: true, setupFiles: ["./src/test-setup.ts"] } });
