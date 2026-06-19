import { defineConfig } from "@rspress/core";

export default defineConfig({
  root: "docs",
  title: "nodeup",
  description: "Node.js version manager documentation",
  route: {
    cleanUrls: true,
  },
  themeConfig: {
    socialLinks: [
      {
        icon: "github",
        mode: "link",
        content: "https://github.com/delinoio/oss",
      },
    ],
    sidebar: {
      "/": [
        {
          text: "Guide",
          items: [
            {
              text: "Overview",
              link: "/",
            },
            {
              text: "Getting Started",
              link: "/guide/getting-started",
            },
          ],
        },
      ],
    },
  },
});
