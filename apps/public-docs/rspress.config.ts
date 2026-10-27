import { defineConfig } from "@rspress/core";

const homePages = [
  { text: "Overview", link: "/", activeMatch: "^/$" },
  { text: "Getting Started", link: "/getting-started" },
  { text: "Projects Overview", link: "/projects-overview" },
  { text: "Documentation Lifecycle", link: "/documentation-lifecycle" },
  { text: "Linux Packages", link: "/linux-packages" },
];

const productPages = [
  { text: "Cargo Mono", link: "/cargo-mono" },
  { text: "With Watch", link: "/with-watch" },
];

const runmoorPages = [
  { text: "Overview", link: "/runmoor/", activeMatch: "^/runmoor/$" },
  { text: "Install and Verify", link: "/runmoor/install" },
  { text: "Configuration", link: "/runmoor/configuration" },
  { text: "Commands and Routing", link: "/runmoor/commands" },
  { text: "Docker", link: "/runmoor/docker" },
  { text: "Tart Images", link: "/runmoor/tart" },
  { text: "macOS Host Runners", link: "/runmoor/host" },
  { text: "Operations and Recovery", link: "/runmoor/operations" },
];

const nodeupPages = [
  { text: "Overview", link: "/nodeup/", activeMatch: "^/nodeup/$" },
  { text: "Installation", link: "/nodeup/installation" },
  { text: "Getting Started", link: "/nodeup/getting-started" },
  { text: "Commands", link: "/nodeup/commands" },
  { text: "Runtime Resolution", link: "/nodeup/runtime-resolution" },
  {
    text: "Shims and Package Managers",
    link: "/nodeup/shims-and-package-managers",
  },
  { text: "Output", link: "/nodeup/output" },
  { text: "Completions", link: "/nodeup/completions" },
  { text: "Releases", link: "/nodeup/releases" },
  { text: "Troubleshooting", link: "/nodeup/troubleshooting" },
  { text: "Reference", link: "/nodeup/reference" },
];

const binpmPages = [
  { text: "Overview", link: "/binpm/", activeMatch: "^/binpm/$" },
  { text: "Installation", link: "/binpm/installation" },
  { text: "Getting Started", link: "/binpm/getting-started" },
  { text: "Commands", link: "/binpm/commands" },
  { text: "Local Tooling", link: "/binpm/local-tooling" },
  { text: "Cache and Verification", link: "/binpm/cache-and-verification" },
  { text: "Releases", link: "/binpm/releases" },
  { text: "Troubleshooting", link: "/binpm/troubleshooting" },
  { text: "Reference", link: "/binpm/reference" },
];

const asyncCommitHookPages = [
  {
    text: "Overview",
    link: "/async-commit-hook/",
    activeMatch: "^/async-commit-hook/$",
  },
  { text: "Installation", link: "/async-commit-hook/install" },
  { text: "Get started", link: "/async-commit-hook/start" },
  { text: "Configuration", link: "/async-commit-hook/configuration" },
  { text: "Final validation", link: "/async-commit-hook/validation" },
  { text: "CLI reference", link: "/async-commit-hook/commands" },
  { text: "Agents and MCP", link: "/async-commit-hook/agents" },
  { text: "Browser connection", link: "/async-commit-hook/web" },
  { text: "Privacy and trust", link: "/async-commit-hook/privacy" },
  { text: "Operations and recovery", link: "/async-commit-hook/recovery" },
  {
    text: "Compatibility and validation evidence",
    link: "/async-commit-hook/compatibility",
  },
  { text: "Committed symbolic links", link: "/async-commit-hook/symlinks" },
  {
    text: "Existing hooks and hook managers",
    link: "/async-commit-hook/existing-hooks",
  },
];

const cliboxPages = [
  { text: "Overview", link: "/clibox/", activeMatch: "^/clibox/$" },
  { text: "Install", link: "/clibox/install" },
  { text: "Getting Started", link: "/clibox/getting-started" },
  { text: "Command Index", link: "/clibox/commands" },
  { text: "File Access", link: "/clibox/fspy" },
  { text: "System Commands", link: "/clibox/system" },
  { text: "Text, Time, Base64, and Hashes", link: "/clibox/transformations" },
  { text: "Readiness Waits", link: "/clibox/wait" },
  { text: "Configuration", link: "/clibox/configuration" },
  { text: "Output and Cancellation", link: "/clibox/output" },
  { text: "Migration", link: "/clibox/migration" },
  { text: "Releases and Verification", link: "/clibox/releases" },
  { text: "Troubleshooting", link: "/clibox/troubleshooting" },
];

const pnportPages = [
  { text: "Overview", link: "/pnport/", activeMatch: "^/pnport/$" },
  { text: "Installation and Availability", link: "/pnport/installation" },
  { text: "Getting Started", link: "/pnport/getting-started" },
  { text: "Preview Testing", link: "/pnport/preview-testing" },
  { text: "Commands", link: "/pnport/commands" },
  {
    text: "Filesystem and Processes",
    link: "/pnport/filesystem-and-processes",
  },
  { text: "Editors and Language Servers", link: "/pnport/editors" },
  { text: "Cache Management", link: "/pnport/cache" },
  { text: "Diagnostics and Troubleshooting", link: "/pnport/diagnostics" },
  { text: "Benchmarks", link: "/pnport/benchmarks" },
  { text: "Releases and Rollback", link: "/pnport/releases" },
];

const aiprouterPages = [
  { text: "Overview", link: "/aiprouter/", activeMatch: "^/aiprouter/$" },
  { text: "Clients", link: "/aiprouter/guide/clients" },
  { text: "Compatibility", link: "/aiprouter/guide/compatibility" },
  { text: "Configuration", link: "/aiprouter/guide/configuration" },
  { text: "Installation", link: "/aiprouter/guide/installation" },
  { text: "Migration", link: "/aiprouter/guide/migration" },
  {
    text: "Native compatibility",
    link: "/aiprouter/guide/native-compatibility",
  },
  { text: "Operations", link: "/aiprouter/guide/operations" },
  { text: "Organizations", link: "/aiprouter/guide/organizations" },
  { text: "Providers", link: "/aiprouter/guide/providers" },
  { text: "Recovery", link: "/aiprouter/guide/recovery" },
];

const projectPages = [
  { text: "AIPRouter", link: "/aiprouter/", activeMatch: "^/aiprouter(?:/|$)" },
  { text: "Runmoor", link: "/runmoor/", activeMatch: "^/runmoor" },
  { text: "Nodeup", link: "/nodeup/", activeMatch: "^/nodeup" },
  { text: "binpm", link: "/binpm/", activeMatch: "^/binpm" },
  {
    text: "async-commit-hook",
    link: "/async-commit-hook/",
    activeMatch: "^/async-commit-hook",
  },
  { text: "clibox", link: "/clibox/", activeMatch: "^/clibox(?:/|$)" },
  { text: "pnport", link: "/pnport/", activeMatch: "^/pnport(?:/|$)" },
];

export default defineConfig({
  title: "Delino Public Docs",
  description: "Public documentation for Delino OSS projects.",
  base: "/",
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
    nav: [],
    sidebar: {
      "/": [
        { text: "Get Started", items: homePages.slice(0, 2) },
        { text: "Reference", items: homePages.slice(2) },
        {
          text: "Rust Monorepo Tooling",
          items: [productPages[0]],
        },
        {
          text: "Command Rerun Watcher",
          items: [productPages[1]],
        },
      ],
      "/aiprouter/": [{ text: "AIPRouter", items: aiprouterPages }],
      "/clibox/": [{ text: "clibox", items: cliboxPages }],
      "/pnport/": [{ text: "pnport", items: pnportPages }],
      "/runmoor/": [{ text: "Runmoor", items: runmoorPages }],
      "/nodeup/": [{ text: "Nodeup", items: nodeupPages }],
      "/binpm/": [{ text: "binpm", items: binpmPages }],
      "/async-commit-hook/": [
        { text: "async-commit-hook", items: asyncCommitHookPages },
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
