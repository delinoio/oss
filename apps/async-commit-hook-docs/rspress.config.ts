import { defineConfig } from "@rspress/core";
import routes from "./routes.json";

export default defineConfig({
  title: "ach Docs",
  description: "Local asynchronous checks against committed source, for developers and coding agents.",
  root: "docs",
  outDir: "doc_build",
  builderConfig: { server: { strictPort: process.env.DELINO_RSPRESS_STRICT_PORT === "1", publicDir: { name: "public" } } },
  route: { cleanUrls: true },
  themeConfig: {
    nav: routes,
    sidebar: { "/": [{ text: "async-commit-hook", items: routes }] },
    socialLinks: [{ icon: "github", mode: "link", content: "https://github.com/delinoio/oss" }],
    footer: { message: 'ach documentation is maintained in the <a href="https://github.com/delinoio/oss">Delino OSS repository</a>.' },
  },
});
