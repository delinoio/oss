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
  assert.doesNotMatch(html, /name="robots" content="noindex"/);
  assert.ok(html.includes('id="home-title"'));
  assert.ok(html.includes('id="products"'));
  assert.ok(html.includes(`href="/${prefix}tairouter/"`));
  assert.ok(html.includes('href="https://docs.labs.delino.io/"'));
  assert.ok(html.includes('href="https://docs.labs.delino.io/tairouter/"'));
  assert.ok(html.includes('href="https://github.com/delinoio/oss"'));
  for (const tool of ["binpm", "nodeup", "cargo-mono"]) {
    assert.ok(
      !html.includes(tool),
      `${lang}: unexpected developer-tool promotion`,
    );
  }
  assert.ok(
    html.includes('hreflang="x-default" href="https://labs.delino.io/"'),
  );
  assert.ok(
    html.includes(
      'property="og:url" content="https://labs.delino.io/' + prefix + '"',
    ),
  );
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
  for (const message of [
    "heroTeam",
    "heroAI",
    "heroRules",
    "homeDescription",
    "homeRouterDescription",
    "homeClosingTitle",
  ]) {
    assert.ok(
      html.includes(escapeHtml(messages[message])),
      `${lang}: missing landing message ${message}`,
    );
  }
  assert.ok(
    html.includes(
      `name="description" content="${escapeHtml(messages.homeDescription)}"`,
    ),
  );
  for (const alternate of registry.locales) {
    const localePrefix =
      alternate.lang === registry.sourceLang ? "" : `${alternate.lang}/`;
    assert.ok(
      html.includes(
        `hreflang="${alternate.lang}" href="https://labs.delino.io/${localePrefix}"`,
      ),
    );
    // Language selection must stay on the home page, including without JavaScript.
    assert.ok(
      html.includes(
        `href="/${localePrefix}" lang="${alternate.lang}" hreflang="${alternate.lang}"`,
      ),
    );
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

assert.equal(
  await readFile(new URL("../dist/robots.txt", import.meta.url), "utf8"),
  "User-agent: *\nAllow: /\n",
);
