import { defineConfig } from "@rspress/core";

const stableDocsRoutes = [
  {
    text: "Overview",
    link: "/",
  },
  {
    text: "Installation",
    link: "/installation",
  },
  {
    text: "Getting Started",
    link: "/getting-started",
  },
  {
    text: "Commands",
    link: "/commands",
  },
  {
    text: "Local Tooling",
    link: "/local-tooling",
  },
  {
    text: "Cache and Verification",
    link: "/cache-and-verification",
  },
  {
    text: "Releases",
    link: "/releases",
  },
  {
    text: "Troubleshooting",
    link: "/troubleshooting",
  },
  {
    text: "Reference",
    link: "/reference",
  },
];

export default defineConfig({
  title: "binpm Docs",
  description: "Documentation for the binpm binary package manager.",
  root: "docs",
  outDir: "doc_build",
  route: {
    cleanUrls: true,
  },
  themeConfig: {
    nav: [
      {
        text: "Overview",
        link: "/",
      },
      {
        text: "Docs",
        items: stableDocsRoutes.slice(1),
      },
    ],
    sidebar: {
      "/": [
        {
          text: "binpm",
          items: stableDocsRoutes,
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
        'binpm documentation is maintained in the <a href="https://github.com/delinoio/oss">Delino OSS repository</a>.',
    },
  },
});
