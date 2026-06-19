import { defineConfig } from "@rspress/core";

export default defineConfig({
  title: "Nodeup Docs",
  description: "Documentation for the Nodeup Node.js version manager.",
  root: "docs",
  outDir: "doc_build",
  themeConfig: {
    nav: [
      {
        text: "Guide",
        link: "/getting-started",
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
              text: "Getting Started",
              link: "/getting-started",
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
      message: "Nodeup documentation is maintained in the Delino OSS monorepo.",
    },
  },
});
