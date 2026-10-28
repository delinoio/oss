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
  const product = await readFile(
    new URL(`../dist/${prefix}tairouter/index.html`, import.meta.url),
    "utf8",
  );
  assert.ok(product.includes(`lang="${lang}"`));
  assert.ok(product.includes(`<title>${messages.productTitle}</title>`));
  assert.ok(
    product.includes(`href="https://labs.delino.io/${prefix}tairouter/"`),
  );
  assert.doesNotMatch(product, /name="robots" content="noindex"/);
  assert.ok(product.includes('id="hero-title"'));
  assert.ok(product.includes('id="features"'));
  assert.ok(product.includes('id="clients-title"'));
  assert.ok(product.includes('id="closing-title"'));
  assert.ok(product.includes('href="https://docs.labs.delino.io/tairouter/"'));
  assert.ok(product.includes('href="https://github.com/delinoio/oss"'));
  assert.ok(product.includes("Team AI Router"));
  const escapeHtml = (text) =>
    text.replace(
      /[&<>"']/g,
      (character) =>
        ({
          "&": "&amp;",
          "<": "&lt;",
          ">": "&gt;",
          '"': "&quot;",
          "'": "&#39;",
        })[character],
    );
  for (const message of [
    "heroTeam",
    "heroAI",
    "heroRules",
    "accountsTitle",
    "releaseNote",
  ]) {
    assert.ok(
      product.includes(escapeHtml(messages[message])),
      `${lang}: missing ${message}`,
    );
  }
  for (const alternate of registry.locales) {
    const localePrefix =
      alternate.lang === registry.sourceLang ? "" : `${alternate.lang}/`;
    assert.ok(
      product.includes(
        `hreflang="${alternate.lang}" href="https://labs.delino.io/${localePrefix}tairouter/"`,
      ),
    );
  }
  assert.doesNotMatch(product, /api\.openai\.com|accessToken|refreshToken/);
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
