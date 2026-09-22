import { defineConfig } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
import { allowLocalProxy } from "./src/dev-proxy";
export default defineConfig({
  plugins: [pluginReact()],
  source: { entry: { index: "./src/main.tsx" } },
  html: { template: "./index.html" },
  server: {
    host: "localhost", port: 46308, strictPort: true, cors: false,
    proxy: {
      "/async_commit_hook.v1.LocalService/": {
        target: "http://127.0.0.1:46309",
        changeOrigin: true,
        bypass(request) {
          if (!allowLocalProxy(request)) return false;
          request.headers.origin = "http://127.0.0.1:46309";
        },
      },
    },
  },
  output: {
    sourceMap: false,
    distPath: { js: "", css: "" },
    filename: {
      js: "assets/[name].[contenthash:8].js",
      css: "assets/[name].[contenthash:8].css",
    },
  },
});
