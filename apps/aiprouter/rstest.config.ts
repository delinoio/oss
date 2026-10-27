// SPDX-License-Identifier: Apache-2.0
import { pluginReact } from "@rsbuild/plugin-react";
import { defineConfig } from "@rstest/core";
export default defineConfig({
  plugins: [pluginReact()],
  include: ["src/**/*.test.{ts,tsx}", "test/**/*.test.{ts,tsx}"],
  testEnvironment: "jsdom",
  setupFiles: ["./test/setup.ts"],
  restoreMocks: true,
});
