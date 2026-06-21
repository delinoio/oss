import { defineConfig } from "@rspress/core";

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
        text: "Install",
        link: "/installation",
      },
      {
        text: "Guide",
        link: "/getting-started",
      },
      {
        text: "Commands",
        link: "/commands",
      },
      {
        text: "Reference",
        link: "/reference",
      },
    ],
    sidebar: {
      "/": [
        {
          text: "binpm",
          items: [
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
              text: "Troubleshooting",
              link: "/troubleshooting",
            },
            {
              text: "Reference",
              link: "/reference",
            },
          ],
        },
      ],
    },
    footer: {
      message: "binpm documentation is maintained in the Delino OSS monorepo.",
    },
  },
});
