import { access, readdir, readFile, stat } from "node:fs/promises";
import path from "node:path";

const documentedRouteIds = [
  "/",
  "/installation",
  "/getting-started",
  "/commands",
  "/local-tooling",
  "/cache-and-verification",
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

const routeOutputFiles = documentedRouteIds.map((routeId) => ({
  routeId,
  outputFile:
    routeId === "/"
      ? path.join(outputDir, "index.html")
      : path.join(outputDir, `${routeId.slice(1)}.html`),
}));

const cleanHrefPatterns = documentedRouteIds.map((routeId) => ({
  routeId,
  pattern: new RegExp(
    `href=(["'])(?:https?:\\/\\/[^/"']+)?${routeId === "/" ? "/" : routeId}(?:[?#][^"']*)?\\1`,
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

const htmlContents = await Promise.all(
  htmlFiles.map(async (htmlFile) => ({
    htmlFile,
    contents: await readFile(htmlFile, "utf8"),
  })),
);

for (const { routeId, pattern } of cleanHrefPatterns) {
  if (!htmlContents.some(({ contents }) => pattern.test(contents))) {
    failures.push(`${routeId} was not linked as a clean URL in generated HTML`);
  }
}

if (failures.length > 0) {
  console.error("binpm docs clean URL validation failed:");
  for (const failure of failures) {
    console.error(`- ${failure}`);
  }
  process.exit(1);
}

console.log("binpm docs clean URL validation passed.");
