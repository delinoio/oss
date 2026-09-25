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
  { text: "Overview", link: "/runmoor/", activeMatch: "^/runmoor/$" },
  { text: "Install and Verify", link: "/runmoor/install" },
  { text: "Configuration", link: "/runmoor/configuration" },
  { text: "Commands and Routing", link: "/runmoor/commands" },
  { text: "Docker", link: "/runmoor/docker" },
  { text: "Tart Images", link: "/runmoor/tart" },
  { text: "Operations and Recovery", link: "/runmoor/operations" },
];

const nodeupPages = [
  { text: "Overview", link: "/nodeup/", activeMatch: "^/nodeup/$" },
  { text: "Installation", link: "/nodeup/installation" },
  { text: "Getting Started", link: "/nodeup/getting-started" },
  { text: "Commands", link: "/nodeup/commands" },
  { text: "Runtime Resolution", link: "/nodeup/runtime-resolution" },
  { text: "Shims and Package Managers", link: "/nodeup/shims-and-package-managers" },
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
  { text: "Overview", link: "/async-commit-hook/", activeMatch: "^/async-commit-hook/$" },
  { text: "Installation", link: "/async-commit-hook/install" },
  { text: "Get started", link: "/async-commit-hook/start" },
  { text: "Configuration", link: "/async-commit-hook/configuration" },
  { text: "Final validation", link: "/async-commit-hook/validation" },
  { text: "CLI reference", link: "/async-commit-hook/commands" },
  { text: "Agents and MCP", link: "/async-commit-hook/agents" },
  { text: "Browser connection", link: "/async-commit-hook/web" },
  { text: "Privacy and trust", link: "/async-commit-hook/privacy" },
  { text: "Operations and recovery", link: "/async-commit-hook/recovery" },
  { text: "Compatibility and validation evidence", link: "/async-commit-hook/compatibility" },
  { text: "Committed symbolic links", link: "/async-commit-hook/symlinks" },
  { text: "Existing hooks and hook managers", link: "/async-commit-hook/existing-hooks" },
];

const cliboxPages = [
  { text: "Overview", link: "/clibox/", activeMatch: "^/clibox/$" },
  { text: "Install", link: "/clibox/install" },
  { text: "Getting Started", link: "/clibox/getting-started" },
  { text: "Command Index", link: "/clibox/commands" },
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
  { text: "Commands", link: "/pnport/commands" },
  { text: "Filesystem and Processes", link: "/pnport/filesystem-and-processes" },
  { text: "Editors and Language Servers", link: "/pnport/editors" },
  { text: "Cache Management", link: "/pnport/cache" },
  { text: "Diagnostics and Troubleshooting", link: "/pnport/diagnostics" },
  { text: "Benchmarks", link: "/pnport/benchmarks" },
  { text: "Releases and Rollback", link: "/pnport/releases" },
];

const reactForgeGroups = [
  { text: "Start", items: [
    { text: "Overview", link: "/react-forge/", activeMatch: "^/react-forge/$" },
    { text: "Installation", link: "/react-forge/installation" },
    { text: "Getting Started", link: "/react-forge/getting-started" },
    { text: "Sessions and Common API", link: "/react-forge/sessions" },
  ] },
  { text: "Create Documents", items: [
    { text: "PPTX", link: "/react-forge/formats/pptx/" },
    { text: "DOCX", link: "/react-forge/formats/docx/" },
    { text: "XLSX", link: "/react-forge/formats/xlsx/" },
    { text: "PDF", link: "/react-forge/formats/pdf/" },
  ] },
  { text: "Edit and Automate", items: [
    { text: "Office Editing", link: "/react-forge/office-editing" },
    { text: "Figma Design", link: "/react-forge/formats/figma/" },
    { text: "CLI", link: "/react-forge/cli" },
    { text: "Local MCP", link: "/react-forge/mcp" },
  ] },
  { text: "Unreleased Previews", items: [
    { text: "Game SFX", link: "/react-forge/formats/sfx/" },
    { text: "Pixel Sprites", link: "/react-forge/formats/sprite/" },
  ] },
  { text: "Help", items: [
    { text: "Limits and Troubleshooting", link: "/react-forge/limits-and-troubleshooting" },
    { text: "Releases and Validation", link: "/react-forge/releases" },
  ] },
];

const projectPages = [
  { text: "Runmoor", link: "/runmoor/", activeMatch: "^/runmoor" },
  { text: "Nodeup", link: "/nodeup/", activeMatch: "^/nodeup" },
  { text: "binpm", link: "/binpm/", activeMatch: "^/binpm" },
  { text: "async-commit-hook", link: "/async-commit-hook/", activeMatch: "^/async-commit-hook" },
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
      "/clibox/": [{ text: "clibox", items: cliboxPages }],
      "/pnport/": [{ text: "pnport", items: pnportPages }],
      "/react-forge/": reactForgeGroups,
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
