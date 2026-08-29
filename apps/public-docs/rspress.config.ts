import { defineConfig } from "@rspress/core";

const homePages = [
  { text: "Overview", link: "/", activeMatch: "^/$" },
  { text: "Getting Started", link: "/getting-started" },
  { text: "Projects Overview", link: "/projects-overview" },
  { text: "Documentation Lifecycle", link: "/documentation-lifecycle" },
];

const productPages = [
  { text: "DevHud", link: "/devhud", activeMatch: "^/devhud" },
  { text: "Cargo Mono", link: "/cargo-mono" },
  { text: "Derun", link: "/derun" },
  { text: "With Watch", link: "/with-watch" },
];

export default defineConfig({
  title: "Delino Public Docs",
  description: "Public documentation for Delino OSS projects.",
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
          text: "Developer Utility",
          items: [
            { text: "Overview", link: "/devhud" },
            { text: "Install and Verify", link: "/devhud/install" },
            { text: "Using DevHud", link: "/devhud/guide" },
            { text: "Privacy", link: "/devhud/privacy" },
            { text: "Security", link: "/devhud/security" },
            { text: "Support", link: "/devhud/support" },
            { text: "Administration", link: "/devhud/admin" },
            { text: "Releases", link: "/devhud/releases" },
          ],
        },
        {
          text: "Rust Monorepo Tooling",
          items: [productPages[1]],
        },
        {
          text: "Terminal Relay + MCP",
          items: [productPages[2]],
        },
        {
          text: "Command Rerun Watcher",
          items: [productPages[3]],
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
