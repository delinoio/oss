// SPDX-License-Identifier: Apache-2.0
import { defineConfig } from "@rspress/core";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import registry from "./i18n/locales.json";
import tairouterPages from "./scripts/tairouter-pages.json";

const themeText: Record<string, Record<string, string>> = {};
const locales = registry.locales.map((locale) => {
  const messages: Record<string, string> = JSON.parse(
    readFileSync(
      new URL(`./i18n/messages/${locale.lang}.json`, import.meta.url),
      "utf8",
    ),
  );
  for (const [key, value] of Object.entries(messages)) {
    if (key.startsWith("theme."))
      (themeText[key.slice(6)] ??= {})[locale.lang] = value;
  }
  const prefix = locale.lang === registry.sourceLang ? "" : `/${locale.lang}`;
  return {
    ...locale,
    title: messages.title,
    description: messages.description,
    sidebar: {
      [`${prefix}/`]: [
        {
          text: messages.guide,
          items: [
            {
              text: messages.overview,
              link: `${prefix}/`,
              activeMatch: `^${prefix}/$`,
            },
            {
              text: messages.gettingStarted,
              link: `${prefix}/getting-started`,
            },
          ],
        },
        {
          text: messages.tairouter,
          items: tairouterPages.map(({ page, label }) => ({
            text: messages[label],
            // Rspress prefixes root-relative sidebar links with the active
            // locale. An absolute URL keeps English-only guides on real routes.
            link: `${prefix ? "https://docs.labs.delino.io" : ""}${page === "index" ? "/tairouter/" : `/tairouter/${page}`}`,
            ...(page === "index" ? { activeMatch: "^/tairouter/$" } : {}),
          })),
        },
      ],
    },
  };
});

export default defineConfig({
  title: "Delino Labs",
  description: "Documentation for Delino Labs.",
  root: "docs",
  base: "/",
  outDir: "doc_build",
  lang: registry.sourceLang,
  locales: locales.map(({ lang, label, title, description }) => ({
    lang,
    label,
    title,
    description,
  })),
  i18nSource: (defaults) => {
    for (const key of Object.keys(defaults)) {
      if (registry.locales.some(({ lang }) => !themeText[key]?.[lang])) {
        throw new Error(`Missing theme translation: theme.${key}`);
      }
    }
    for (const [key, translations] of Object.entries(themeText))
      defaults[key] = { ...defaults[key], ...translations };
    return defaults;
  },
  builderConfig: {
    tools: {
      rspack: (config) => {
        config.module ??= {};
        config.module.rules ??= [];
        config.module.rules.push({
          test: /[\\/]@rspress[\\/]core[\\/]dist[\\/]theme[\\/]components[\\/]Nav[\\/]hooks\.js$/,
          use: [
            {
              loader: fileURLToPath(
                new URL("./scripts/english-guides-loader.cjs", import.meta.url),
              ),
            },
          ],
        });
      },
    },
    server: {
      strictPort: process.env.DELINO_RSPRESS_STRICT_PORT === "1",
    },
  },
  route: { cleanUrls: true, localeRedirect: "never" },
  head: [
    (route) => [
      "link",
      {
        rel: "canonical",
        href: `https://docs.labs.delino.io${route.routePath.replace(/\.html$/, "")}`,
      },
    ],
  ],
  themeConfig: { locales },
});
