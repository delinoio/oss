// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { decodeHTML } from "entities";

const origin = "https://docs.labs.delino.io";
const registry = JSON.parse(
  await readFile(new URL("../i18n/locales.json", import.meta.url), "utf8"),
);
const routes = [];
const tairouterPages = JSON.parse(
  await readFile(new URL("./tairouter-pages.json", import.meta.url), "utf8"),
);
for (const { lang } of registry.locales) {
  const prefix = lang === registry.sourceLang ? "" : `/${lang}`;
  for (const page of ["index", "getting-started"]) {
    const markdown = await readFile(
      new URL(`../docs/${lang}/${page}.md`, import.meta.url),
      "utf8",
    );
    routes.push({
      path: page === "index" ? `${prefix}/` : `${prefix}/getting-started`,
      file: `${prefix.slice(1)}${prefix ? "/" : ""}${page}.html`,
      heading: markdown.match(/^# (.+)$/m)[1],
      lang,
      next: page === "index" ? `${prefix}/getting-started` : `${prefix}/`,
    });
  }
}

for (const { page } of tairouterPages) {
  const markdown = await readFile(
    new URL(`../docs/en/tairouter/${page}.md`, import.meta.url),
    "utf8",
  );
  routes.push({
    path: page === "index" ? "/tairouter/" : `/tairouter/${page}`,
    file: `tairouter/${page}.html`,
    heading: markdown.match(/^# (.+)$/m)[1],
    lang: registry.sourceLang,
    next: "/tairouter/guide/installation",
  });
}

for (const route of routes) {
  const html = await readFile(
    new URL(`../doc_build/${route.file}`, import.meta.url),
    "utf8",
  );
  assert.ok(
    html.includes(`lang="${route.lang}"`),
    `${route.path}: incorrect language`,
  );
  assert.match(html, /<main[\s>]/, `${route.path}: missing main landmark`);
  const heading = html.match(/<h1\b[^>]*>([\s\S]*?)<\/h1>/)?.[1] ?? "";
  assert.ok(
    decodeHTML(heading.replace(/<[^>]+>/g, "")).includes(route.heading),
    `${route.path}: incorrect heading`,
  );
  assert.ok(
    html.includes(`rel="canonical" href="${origin}${route.path}"`),
    `${route.path}: incorrect canonical URL`,
  );
  const links = [...html.matchAll(/<a\b[^>]*href="([^"]+)"/g)].map((match) =>
    decodeHTML(match[1]),
  );
  assert.ok(
    links.includes(route.next),
    `${route.path}: missing cross-page navigation`,
  );
  for (const href of links) {
    if (!href || href.startsWith("#")) continue;
    const target = new URL(href, `${origin}${route.path}`);
    if (target.origin !== origin) continue;
    assert.ok(
      routes.some(
        (item) =>
          item.path.replace(/\/$/, "") === target.pathname.replace(/\/$/, ""),
      ),
      `${route.path}: unknown or non-clean link ${target.pathname}`,
    );
  }
  console.log(
    JSON.stringify({
      event: "labs_docs_route",
      route: route.path,
      result: "pass",
    }),
  );
}
const notFound = await readFile(
  new URL("../doc_build/404.html", import.meta.url),
  "utf8",
);
assert.match(notFound, /404/);
console.log(
  JSON.stringify({
    event: "labs_docs_routes",
    result: "pass",
    routes: routes.length,
    notFound: true,
  }),
);

for (const name of ["NOTICE", "RSPRESS-LICENSE"]) {
  assert.equal(
    await readFile(
      new URL(`../doc_build/i18n-${name}`, import.meta.url),
      "utf8",
    ),
    await readFile(new URL(`../i18n/${name}`, import.meta.url), "utf8"),
  );
}
