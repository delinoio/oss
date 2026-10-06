import { defineConfig } from "vitest/config";
export default defineConfig({ test: { include: ["src/**/*.test.ts", "src/**/*.test.tsx"], environment: "jsdom", environmentOptions: { jsdom: { url: "http://localhost/" } }, restoreMocks: true, setupFiles: ["./src/test-setup.ts"] } });
