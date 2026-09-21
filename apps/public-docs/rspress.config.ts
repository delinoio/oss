import { defineConfig } from "@rspress/core";

const homePages = [
  { text: "Overview", link: "/", activeMatch: "^/$" },
  { text: "Getting Started", link: "/getting-started" },
  { text: "Projects Overview", link: "/projects-overview" },
  { text: "Documentation Lifecycle", link: "/documentation-lifecycle" },
  { text: "Linux Packages", link: "/linux-packages" },
];

const productPages = [
  { text: "DevHud", link: "/devhud", activeMatch: "^/devhud" },
  { text: "Cargo Mono", link: "/cargo-mono" },
  { text: "Derun", link: "/derun" },
  { text: "With Watch", link: "/with-watch" },
];

const runmoorPages = [
  { text: "Overview", link: "/runmoor", activeMatch: "^/runmoor$" },
  { text: "Install and Verify", link: "/runmoor/install" },
  { text: "Configuration", link: "/runmoor/configuration" },
  { text: "Commands and Routing", link: "/runmoor/commands" },
  { text: "Docker", link: "/runmoor/docker" },
  { text: "Tart Images", link: "/runmoor/tart" },
  { text: "Operations and Recovery", link: "/runmoor/operations" },
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
    // Product switching lives in the accessible header control. Keep the
    // Rspress top navigation empty so the sidebar remains the only article
    // navigation surface on desktop and mobile.
    nav: [],
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
      "/runmoor": [
        { text: "Runmoor", items: runmoorPages },
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
