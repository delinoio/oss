// SPDX-License-Identifier: Apache-2.0
import { defineConfig } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
export default defineConfig({
  plugins: [pluginReact()],
  html: { title: "AIPRouter", lang: "en" },
  server: {
    host: "127.0.0.1",
    port: 4173,
    strictPort: true,
    proxy: {
      "/aiprouter.v1.": { target: "http://127.0.0.1:8080" },
      "/auth": { target: "http://127.0.0.1:8080" },
    },
  },
});
