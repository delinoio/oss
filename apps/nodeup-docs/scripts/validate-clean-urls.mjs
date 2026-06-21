import { readdir, readFile, stat } from "node:fs/promises";
import path from "node:path";

const documentedRouteIds = [
  "/",
  "/installation",
  "/getting-started",
  "/commands",
  "/runtime-resolution",
  "/shims-and-package-managers",
  "/output",
  "/completions",
  "/releases",
  "/troubleshooting",
  "/reference",
];

const outputDir = path.resolve("doc_build");
const htmlHrefPatterns = documentedRouteIds.map((routeId) => {
  if (routeId === "/") {
    return {
      routeId,
      htmlRoute: "/index.html",
      pattern: /href=(["'])(?:https?:\/\/[^/"']+)?\/index\.html(?:[?#][^"']*)?\1/,
    };
  }

  return {
    routeId,
    htmlRoute: `${routeId}.html`,
    pattern: new RegExp(
      `href=(["'])(?:https?:\\/\\/[^/"']+)?${routeId}\\.html(?:[?#][^"']*)?\\1`,
    ),
  };
});

async function collectHtmlFiles(directory) {
  const entries = await readdir(directory);
  const htmlFiles = [];

  for (const entry of entries) {
    const entryPath = path.join(directory, entry);
    const entryStat = await stat(entryPath);

    if (entryStat.isDirectory()) {
      htmlFiles.push(...(await collectHtmlFiles(entryPath)));
      continue;
    }

    if (entryPath.endsWith(".html")) {
      htmlFiles.push(entryPath);
    }
  }

  return htmlFiles;
}

const htmlFiles = await collectHtmlFiles(outputDir);
const failures = [];

for (const htmlFile of htmlFiles) {
  const contents = await readFile(htmlFile, "utf8");

  for (const { htmlRoute, pattern } of htmlHrefPatterns) {
    if (pattern.test(contents)) {
      failures.push(`${path.relative(outputDir, htmlFile)} links to ${htmlRoute}`);
    }
  }
}

if (failures.length > 0) {
  console.error("Nodeup docs build emitted .html hrefs for documented route IDs:");
  for (const failure of failures) {
    console.error(`- ${failure}`);
  }
  process.exit(1);
}

console.log("Nodeup docs clean URL validation passed.");
