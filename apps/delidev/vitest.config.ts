import { defineConfig } from "vitest/config";
export default defineConfig({ test: { include: ["src/**/*.test.ts", "src/**/*.test.tsx"], environment: "jsdom", restoreMocks: true, setupFiles: ["./src/test-setup.ts"] } });
