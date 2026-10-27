// SPDX-License-Identifier: Apache-2.0
import { defineConfig } from "@rsbuild/core";

export default defineConfig({
  source: { entry: { index: "./src/index.ts" } },
  html: { template: "./index.html", title: "Delino Labs" },
  server: { host: "127.0.0.1", port: 46312, strictPort: true, cors: false },
  output: { sourceMap: false },
});
