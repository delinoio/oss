// SPDX-License-Identifier: Apache-2.0
import { defineConfig } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
export default defineConfig({
  plugins: [pluginReact()],
  html: {
    title: "TaiRouter",
    lang: "en",
    meta: {
      "theme-color": "#1d4ed8",
      "apple-mobile-web-app-title": "TaiRouter",
    },
    tags: [
      {
        tag: "link",
        attrs: { rel: "manifest", href: "/manifest.webmanifest" },
        head: true,
        publicPath: false,
      },
      {
        tag: "link",
        attrs: {
          rel: "apple-touch-icon",
          sizes: "180x180",
          href: "/assets/tairouter-180.png",
        },
        head: true,
        publicPath: false,
      },
    ],
  },
  server: {
    host: "127.0.0.1",
    port: 4173,
    strictPort: true,
    proxy: {
      "/tairouter.v1.": { target: "http://127.0.0.1:8080" },
      "/auth": { target: "http://127.0.0.1:8080" },
    },
  },
});
