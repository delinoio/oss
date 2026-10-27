// SPDX-License-Identifier: Apache-2.0
import { defineConfig } from "@rsbuild/core";
import { readFileSync } from "node:fs";
import registry from "./i18n/locales.json";

const entries = registry.locales.map(({ lang }) => {
  const name = lang === registry.sourceLang ? "index" : `${lang}/index`;
  const messages: Record<string, string> = JSON.parse(
    readFileSync(
      new URL(`./i18n/messages/${lang}.json`, import.meta.url),
      "utf8",
    ),
  );
  return { name, lang, title: messages.title };
});

export default defineConfig({
  source: {
    entry: Object.fromEntries(
      entries.map(({ name }) => [name, "./src/index.ts"]),
    ),
  },
  html: {
    template: "./index.html",
    title: ({ entryName }) =>
      entries.find(({ name }) => name === entryName)!.title,
    templateParameters: ({ entryName }) => {
      const entry = entries.find(({ name }) => name === entryName)!;
      return {
        language: entry.lang,
        canonical: `https://labs.delino.io${entry.lang === registry.sourceLang ? "/" : `/${entry.lang}/`}`,
      };
    },
  },
  server: { host: "127.0.0.1", port: 46312, strictPort: true, cors: false },
  output: { sourceMap: false },
});
