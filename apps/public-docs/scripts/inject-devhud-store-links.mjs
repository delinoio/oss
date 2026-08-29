import { readdir, readFile, stat, writeFile } from "node:fs/promises";
import path from "node:path";

const outputDir = "apps/public-docs/doc_build";
const installRoute = path.join(outputDir, "devhud/install.html");
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

const replacements = new Map([
  ["__DEVHUD_APP_STORE_APP_ID__", appStoreId],
  ["__DEVHUD_GOOGLE_PLAY_PACKAGE_NAME__", googlePlayPackage],
  ["__DEVHUD_CHROME_EXTENSION_ID__", chromeExtensionId],
]);

async function collectFiles(directory) {
  const entries = await readdir(directory);
  const files = [];
  for (const entry of entries) {
    const entryPath = path.join(directory, entry);
    if ((await stat(entryPath)).isDirectory()) files.push(...(await collectFiles(entryPath)));
    else files.push(entryPath);
  }
  return files;
}

const generatedFiles = await collectFiles(outputDir);
for (const generatedFile of generatedFiles) {
  let contents = await readFile(generatedFile, "utf8");
  const originalContents = contents;
  for (const [placeholder, value] of replacements) contents = contents.replaceAll(placeholder, value);
  if (contents !== originalContents) await writeFile(generatedFile, contents);
}

const remainingPlaceholders = [];
for (const generatedFile of generatedFiles) {
  const contents = await readFile(generatedFile, "utf8");
  for (const placeholder of replacements.keys()) {
    if (contents.includes(placeholder)) remainingPlaceholders.push(`${placeholder} in ${generatedFile}`);
  }
}
if (remainingPlaceholders.length > 0) {
  throw new Error(`DevHud store-link placeholders remain: ${remainingPlaceholders.join(", ")}`);
}

const html = await readFile(installRoute, "utf8");
for (const url of [
  `https://apps.apple.com/app/id${appStoreId}`,
  `https://play.google.com/store/apps/details?id=${googlePlayPackage}`,
  `https://chromewebstore.google.com/detail/devhud/${chromeExtensionId}`,
]) {
  if (!html.includes(url)) throw new Error(`Published install page is missing ${url}`);
}
