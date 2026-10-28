// SPDX-License-Identifier: Apache-2.0
import { defineConfig } from "@rsbuild/core";
import { readFileSync } from "node:fs";
import registry from "./i18n/locales.json";

const languages = (product: boolean) =>
  registry.locales.map((locale) => ({
    ...locale,
    path: `${locale.lang === registry.sourceLang ? "" : `/${locale.lang}`}/${product ? "tairouter/" : ""}`,
  }));

const escapeHtml = (value: string) =>
  value.replace(
    /[&<>"']/g,
    (character) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        character
      ]!,
  );

const entries = registry.locales.flatMap(({ lang, label }) => {
  const prefix = lang === registry.sourceLang ? "" : `${lang}/`;
  const messages: Record<string, string> = JSON.parse(
    readFileSync(
      new URL(`./i18n/messages/${lang}.json`, import.meta.url),
      "utf8",
    ),
  );
  return [false, true].map((product) => ({
    name: `${prefix}${product ? "tairouter/" : ""}index`,
    lang,
    label,
    product,
    messages,
    title: product ? messages.productTitle : messages.title,
    path: `/${prefix}${product ? "tairouter/" : ""}`,
  }));
});

export default defineConfig({
  source: {
    entry: Object.fromEntries(
      entries.map(({ name }) => [name, "./src/index.ts"]),
    ),
  },
  html: {
    template: ({ entryName }) =>
      entries.find(({ name }) => name === entryName)!.product
        ? "./tairouter.html"
        : "./index.html",
    title: ({ entryName }) =>
      entries.find(({ name }) => name === entryName)!.title,
    templateParameters: ({ entryName }) => {
      const entry = entries.find(({ name }) => name === entryName)!;
      return {
        language: entry.lang,
        canonical: `https://labs.delino.io${entry.path}`,
        homePath: `/${entry.lang === registry.sourceLang ? "" : `${entry.lang}/`}`,
        productPath: `/${entry.lang === registry.sourceLang ? "" : `${entry.lang}/`}tairouter/`,
        languageLabel: entry.label,
        languages: languages(entry.product),
        tools: [
          {
            name: "binpm",
            symbol: "◇",
            description: "homeBinpmDescription",
            href: "https://oss.delino.io/binpm/",
          },
          {
            name: "nodeup",
            symbol: "⬡",
            description: "homeNodeupDescription",
            href: "https://oss.delino.io/nodeup/",
          },
          {
            name: "cargo-mono",
            symbol: "▱",
            description: "homeCargoDescription",
            href: "https://oss.delino.io/cargo-mono/",
          },
        ],
        t: (key: string) => {
          const message = entry.messages[key];
          if (!message)
            throw new Error(`Missing marketing message: ${entry.lang}/${key}`);
          return escapeHtml(message);
        },
      };
    },
  },
  server: { host: "127.0.0.1", port: 46312, strictPort: true, cors: false },
  output: { sourceMap: false },
});
