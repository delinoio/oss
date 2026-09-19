import { defineConfig } from "@rspress/core";

const stableDocsRoutes = [
  { text: "Overview", link: "/", activeMatch: "^/$" },
  { text: "Install and Verify", link: "/install" },
  { text: "Configuration", link: "/configuration" },
  { text: "Commands and Routing", link: "/commands" },
  { text: "Docker", link: "/docker" },
  { text: "Tart Images", link: "/tart" },
  { text: "Operations and Recovery", link: "/operations" },
];

export default defineConfig({
  title: "Runmoor Docs",
  description: "Documentation for Runmoor disposable GitHub Actions runners.",
  root: "docs",
  outDir: "doc_build",
  builderConfig: {
    server: {
      strictPort: process.env.DELINO_RSPRESS_STRICT_PORT === "1",
    },
  },
  route: {
    cleanUrls: true,
  },
  themeConfig: {
    nav: stableDocsRoutes,
    sidebar: {
      "/": [{ text: "Runmoor", items: stableDocsRoutes }],
    },
    socialLinks: [
      {
        icon: "github",
        mode: "link",
        content: "https://github.com/delinoio/oss",
      },
    ],
    footer: {
      message:
        'Runmoor documentation is maintained in the <a href="https://github.com/delinoio/oss">Delino OSS repository</a>.',
    },
  },
});
