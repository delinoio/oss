import { defineConfig } from "vitest/config";
export default defineConfig({
  test: {
    include: ["src/**/*.test.tsx", "src/**/*.test.ts"],
    environment: "jsdom",
    // Node's Web Storage globals can shadow jsdom even without a storage file.
    // Keep browser state in jsdom's isolated memory. Remove this flag
    // once Vitest consistently replaces native storage with the DOM instance.
    execArgv: ["--no-experimental-webstorage"],
    setupFiles: ["./src/test-setup.ts"],
  },
});
