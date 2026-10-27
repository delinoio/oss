// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

const registry = JSON.parse(
  await readFile(new URL("../i18n/locales.json", import.meta.url), "utf8"),
);
for (const { lang } of registry.locales) {
  const prefix = lang === registry.sourceLang ? "" : `${lang}/`;
  const html = await readFile(
    new URL(`../dist/${prefix}index.html`, import.meta.url),
    "utf8",
  );
  const messages = JSON.parse(
    await readFile(
      new URL(`../i18n/messages/${lang}.json`, import.meta.url),
      "utf8",
    ),
  );
  assert.ok(html.includes(`lang="${lang}"`));
  assert.ok(html.includes(`<title>${messages.title}</title>`));
  assert.ok(html.includes(`href="https://labs.delino.io/${prefix}"`));
  assert.match(html, /name="robots" content="noindex"/);
  assert.match(html, /<main id="main-content"><\/main>/);
  assert.doesNotMatch(html, /api\.openai\.com|accessToken|refreshToken/);
  console.log(
    JSON.stringify({
      event: "labs_site_route",
      language: lang,
      result: "pass",
    }),
  );
}
assert.match(
  await readFile(new URL("../dist/404.html", import.meta.url), "utf8"),
  /404/,
);
