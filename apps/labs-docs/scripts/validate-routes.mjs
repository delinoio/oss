// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { decodeHTML } from "entities";

const origin = "https://docs.labs.delino.io";
const routes = [
  {
    path: "/",
    file: "index.html",
    heading: "Delino Labs",
    next: "/getting-started",
  },
  {
    path: "/getting-started",
    file: "getting-started.html",
    heading: "Getting Started",
    next: "/",
  },
];

for (const route of routes) {
  const html = await readFile(
    new URL(`../doc_build/${route.file}`, import.meta.url),
    "utf8",
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
      routes.some((item) => item.path === target.pathname),
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
