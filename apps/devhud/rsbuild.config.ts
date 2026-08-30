import { defineConfig, loadEnv } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
import { createDevHudDevelopmentCsp } from "./scripts/development-csp.mjs";

export const DEVHUD_DEVELOPMENT_CSP = createDevHudDevelopmentCsp(
  process.env.DEVHUD_LOGTO_ISSUER ?? "http://localhost:3001/oidc",
);

const { rawPublicVars } = loadEnv({ prefixes: ["TAURI_"] });
const mobileFrontend = new Set(["android", "ios"]).has(rawPublicVars.TAURI_ENV_PLATFORM ?? "");

export default defineConfig({
  plugins: [pluginReact()],
  source: {
    preEntry: mobileFrontend ? undefined : "./src/realqa-font.css",
    entry: {
      index: mobileFrontend ? "./src/main.mobile.tsx" : "./src/main.desktop.tsx",
    },
  },
  html: {
    template: "./index.html",
  },
  output: {
    cleanDistPath: true,
    distPath: {
      root: "dist",
    },
    filename: {
      css: "assets/[name].[contenthash:8].css",
      js: "assets/[name].[contenthash:8].js",
    },
    sourceMap: false,
  },
  performance: {
    chunkSplit: {
      strategy: "all-in-one",
    },
  },
  server: {
    headers: {
      "Content-Security-Policy": DEVHUD_DEVELOPMENT_CSP,
    },
    host: "127.0.0.1",
    port: 46305,
    strictPort: true,
  },
});
