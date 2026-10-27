// SPDX-License-Identifier: Apache-2.0
import { defineConfig } from "@rspress/core";

export default defineConfig({
  title: "Delino Labs",
  description: "Documentation for Delino Labs.",
  root: "docs",
  base: "/",
  outDir: "doc_build",
  builderConfig: {
    server: {
      strictPort: process.env.DELINO_RSPRESS_STRICT_PORT === "1",
    },
  },
  route: { cleanUrls: true },
  head: [
    (route) => [
      "link",
      {
        rel: "canonical",
        href: `https://docs.labs.delino.io${route.routePath.replace(/\.html$/, "")}`,
      },
    ],
  ],
  themeConfig: {
    sidebar: {
      "/": [
        {
          text: "Guide",
          items: [
            { text: "Overview", link: "/", activeMatch: "^/$" },
            { text: "Getting Started", link: "/getting-started" },
          ],
        },
      ],
    },
  },
});
