import { readFile, writeFile } from "node:fs/promises";

const outputFile = "apps/public-docs/doc_build/devhud/install.html";
const appStoreId = process.env.DEVHUD_APP_STORE_APP_ID ?? "";
const googlePlayPackage = process.env.DEVHUD_GOOGLE_PLAY_PACKAGE_NAME ?? "";
const chromeExtensionId = process.env.DEVHUD_CHROME_EXTENSION_ID ?? "";

if (!/^\d+$/u.test(appStoreId)) throw new Error("DEVHUD_APP_STORE_APP_ID must be numeric");
if (!/^[a-z][a-z0-9_]*(?:\.[a-z0-9_]+)+$/u.test(googlePlayPackage)) {
  throw new Error("DEVHUD_GOOGLE_PLAY_PACKAGE_NAME is not a valid Android package name");
}
if (!/^[a-p]{32}$/u.test(chromeExtensionId)) {
  throw new Error("DEVHUD_CHROME_EXTENSION_ID must contain 32 lowercase a-p characters");
}

let html = await readFile(outputFile, "utf8");
const replacements = new Map([
  ["__DEVHUD_APP_STORE_APP_ID__", appStoreId],
  ["__DEVHUD_GOOGLE_PLAY_PACKAGE_NAME__", googlePlayPackage],
  ["__DEVHUD_CHROME_EXTENSION_ID__", chromeExtensionId],
]);
for (const [placeholder, value] of replacements) html = html.replaceAll(placeholder, value);
if ([...replacements.keys()].some((placeholder) => html.includes(placeholder))) {
  throw new Error("DevHud store-link placeholders remain in the published install page");
}

for (const url of [
  `https://apps.apple.com/app/id${appStoreId}`,
  `https://play.google.com/store/apps/details?id=${googlePlayPackage}`,
  `https://chromewebstore.google.com/detail/devhud/${chromeExtensionId}`,
]) {
  if (!html.includes(url)) throw new Error(`Published install page is missing ${url}`);
}
await writeFile(outputFile, html);
