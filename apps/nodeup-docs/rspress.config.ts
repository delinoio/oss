import { defineConfig } from "@rspress/core";

export default defineConfig({
  title: "Nodeup Docs",
  description: "Documentation for the Nodeup Node.js version manager.",
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
          text: "Nodeup",
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
              text: "Runtime Resolution",
              link: "/runtime-resolution",
            },
            {
              text: "Shims and Package Managers",
              link: "/shims-and-package-managers",
            },
            {
              text: "Output",
              link: "/output",
            },
            {
              text: "Completions",
              link: "/completions",
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
          ],
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
        'Nodeup documentation is maintained in the <a href="https://github.com/delinoio/oss">Delino OSS repository</a>.',
    },
  },
});
