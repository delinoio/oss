import { access, readdir, readFile, stat } from "node:fs/promises";
import path from "node:path";

const stableRouteIds = [
  "/",
  "/getting-started",
  "/projects-overview",
  "/documentation-lifecycle",
  "/cargo-mono",
  "/derun",
  "/with-watch",
  "/nodeup",
];

const outputDir = path.resolve("doc_build");
const routeOutputFiles = stableRouteIds.map((routeId) => ({
  routeId,
  outputFile:
    routeId === "/"
      ? path.join(outputDir, "index.html")
      : path.join(outputDir, `${routeId.slice(1)}.html`),
}));
const htmlHrefPatterns = stableRouteIds.map((routeId) => ({
  htmlRoute: routeId === "/" ? "/index.html" : `${routeId}.html`,
  pattern:
    routeId === "/"
      ? /href=(["'])(?:https?:\/\/[^/"']+)?\/index\.html(?:[?#][^"']*)?\1/
      : new RegExp(
          `href=(["'])(?:https?:\\/\\/[^/"']+)?${routeId}\\.html(?:[?#][^"']*)?\\1`,
        ),
}));

async function pathExists(filePath) {
  try {
    await access(filePath);
    return true;
  } catch {
    return false;
  }
}

async function collectHtmlFiles(directory) {
  const entries = await readdir(directory);
  const htmlFiles = [];

  for (const entry of entries) {
    const entryPath = path.join(directory, entry);
    const entryStat = await stat(entryPath);

    if (entryStat.isDirectory()) {
      htmlFiles.push(...(await collectHtmlFiles(entryPath)));
    } else if (entryPath.endsWith(".html")) {
      htmlFiles.push(entryPath);
    }
  }

  return htmlFiles;
}

const htmlFiles = await collectHtmlFiles(outputDir);
const failures = [];

for (const { routeId, outputFile } of routeOutputFiles) {
  if (!(await pathExists(outputFile))) {
    failures.push(
      `${routeId} was not emitted at ${path.relative(outputDir, outputFile)}`,
    );
  }
}

for (const htmlFile of htmlFiles) {
  const contents = await readFile(htmlFile, "utf8");

  for (const { htmlRoute, pattern } of htmlHrefPatterns) {
    if (pattern.test(contents)) {
      failures.push(`${path.relative(outputDir, htmlFile)} links to ${htmlRoute}`);
    }
  }
}

if (failures.length > 0) {
  console.error("Public docs clean URL validation failed:");
  for (const failure of failures) {
    console.error(`- ${failure}`);
  }
  process.exit(1);
}

console.log("Public docs clean URL validation passed.");
