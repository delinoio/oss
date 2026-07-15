import { defineConfig } from "@rspress/core";

const homePages = [
  { text: "Overview", link: "/", activeMatch: "^/$" },
  { text: "Getting Started", link: "/getting-started" },
  { text: "Projects Overview", link: "/projects-overview" },
  { text: "Documentation Lifecycle", link: "/documentation-lifecycle" },
];

const productPages = [
  { text: "Cargo Mono", link: "/cargo-mono" },
  { text: "Derun", link: "/derun" },
  { text: "With Watch", link: "/with-watch" },
];

export default defineConfig({
  title: "Delino Public Docs",
  description: "Public documentation for Delino OSS projects.",
  root: "docs",
  outDir: "doc_build",
  route: {
    cleanUrls: true,
  },
  themeConfig: {
    nav: [
      ...homePages,
      ...productPages,
      { text: "Nodeup", link: "https://nodeup.delino.io" },
      { text: "binpm", link: "https://binpm.delino.io" },
    ],
    sidebar: {
      "/": [
        { text: "Get Started", items: homePages.slice(0, 2) },
        { text: "Reference", items: homePages.slice(2) },
        {
          text: "Rust Monorepo Tooling",
          items: [productPages[0]],
        },
        {
          text: "Terminal Relay + MCP",
          items: [productPages[1]],
        },
        {
          text: "Command Rerun Watcher",
          items: [productPages[2]],
        },
      ],
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
        'Public documentation is maintained in the <a href="https://github.com/delinoio/oss">Delino OSS repository</a>.',
    },
  },
});
