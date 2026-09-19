import { defineConfig } from "@rspress/core";

const stableDocsRoutes = [
  {
    "text": "Overview",
    "link": "/"
  },
  {
    "text": "Installation",
    "link": "/installation"
  },
  {
    "text": "Configuration",
    "link": "/configuration"
  },
  {
    "text": "Commands",
    "link": "/commands"
  },
  {
    "text": "Verification",
    "link": "/verification"
  },
  {
    "text": "Reports",
    "link": "/reports"
  },
  {
    "text": "Privacy",
    "link": "/privacy"
  },
  {
    "text": "Platforms",
    "link": "/platforms"
  },
  {
    "text": "Troubleshooting",
    "link": "/troubleshooting"
  },
  {
    "text": "Benchmarks",
    "link": "/benchmarks"
  },
  {
    "text": "Releases",
    "link": "/releases"
  }
];

export default defineConfig({
  title: "Runlens Docs",
  description: "Filesystem diagnostics, clean verification, and metadata reports for developer commands.",
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
      "/": [{ text: "Runlens", items: stableDocsRoutes }],
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
        'Runlens documentation is maintained in the <a href="https://github.com/delinoio/oss">Delino OSS repository</a>.',
    },
  },
});
