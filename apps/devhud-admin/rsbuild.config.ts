import { defineConfig } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
import { ADMIN_CSP, developmentAdminCsp } from "./src/csp";

export const ADMIN_PORT = 46306;

export default defineConfig(({ command }) => ({
  plugins: [pluginReact()],
  source: { entry: { index: "./src/main.tsx" } },
  html: { template: "./index.html" },
  output: {
    assetPrefix: "/admin/",
    cleanDistPath: true,
    distPath: { root: "dist" },
    filename: {
      css: "assets/[name].[contenthash:8].css",
      js: "assets/[name].[contenthash:8].js",
    },
    sourceMap: false,
  },
  performance: { chunkSplit: { strategy: "all-in-one" } },
  server: {
    host: "localhost",
    port: ADMIN_PORT,
    strictPort: true,
    headers: {
      "Content-Security-Policy":
        command === "dev"
          ? developmentAdminCsp(import.meta.env.DEVHUD_LOGTO_ISSUER)
          : ADMIN_CSP,
    },
  },
}));
