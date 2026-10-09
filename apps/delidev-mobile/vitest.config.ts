import { defineConfig } from "vitest/config";
export default defineConfig({ test: { environment: "jsdom", maxWorkers: 2, restoreMocks: true, include: ["src/**/*.test.ts", "src/**/*.test.tsx"], testTimeout: 15000 } });
