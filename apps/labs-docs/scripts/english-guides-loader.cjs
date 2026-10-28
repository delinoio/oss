// SPDX-License-Identifier: Apache-2.0
const registry = require("../i18n/locales.json");

module.exports = function englishGuidesLoader(source) {
  this.addDependency(require.resolve("../i18n/locales.json"));
  const signature =
    "function replaceLang(rawUrl, lang, version, cleanUrls, isPageNotFound) {";
  if (!source.includes(signature)) {
    throw new Error(
      "The Rspress language-menu hook changed; review English-only guide navigation.",
    );
  }
  const routes = registry.sourceOnly.pages.map(
    (page) => `/${page.replace(/\.md$/, "").replace(/\/index$/, "")}`,
  );
  // Rspress 2.0.23 guesses a translated URL even when that page does not exist.
  // Keep its default menu and route only these English-only guides to the target
  // language overview, for both SSR and hydration. Remove this adapter when the
  // stock theme handles missing translations or these guides are translated.
  return source.replace(
    signature,
    `${signature}
    const englishOnlyRoutes = ${JSON.stringify(routes)};
    const currentPath = removeBase(rawUrl).split(/[?#]/)[0].replace(/\\/$/, "");
    if (lang.target !== lang.default && englishOnlyRoutes.includes(currentPath)) {
      return "/" + lang.target + "/";
    }
  `,
  );
};
