import { access, readdir, readFile, stat } from "node:fs/promises";
import path from "node:path";

const stableRouteIds = [
  "/",
  "/getting-started",
  "/projects-overview",
  "/documentation-lifecycle",
  "/devhud",
  "/devhud/install",
  "/devhud/guide",
  "/devhud/privacy",
  "/devhud/security",
  "/devhud/support",
  "/devhud/admin",
  "/devhud/releases",
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
const requiredHeadings = new Map([
  ["/devhud", ["DevHud"]],
  ["/devhud/install", ["Install and Verify DevHud", "Desktop", "Mobile stores", "Chrome extension"]],
  ["/devhud/guide", ["Using DevHud", "First run and identity", "Settings and PAT profiles", "Capture, drafts, and browser context", "Decks and widgets"]],
  ["/devhud/privacy", ["DevHud Privacy", "What is stored", "Public images", "Account deletion and recovery", "Diagnostics"]],
  ["/devhud/security", ["DevHud Security", "Secure operation", "Updates and key rotation", "Custom API origins", "Reporting"]],
  ["/devhud/support", ["DevHud Support", "Troubleshooting", "Uninstall", "Help and reports"]],
  ["/devhud/admin", ["DevHud Administration"]],
  ["/devhud/releases", ["DevHud Releases"]],
]);
const requiredLinks = new Map([
  ["/devhud", ["/devhud/install", "/devhud/privacy", "/devhud/security", "/devhud/support"]],
  ["/devhud/install", ["/devhud/releases", "/devhud/security", "/devhud/support"]],
  ["/devhud/guide", ["/devhud/privacy", "/devhud/security", "/devhud/support"]],
]);

function articleContent(contents) {
  const article = contents.match(/<article\b[^>]*>[\s\S]*?<\/article>/iu);
  if (article) return article[0];
  const main = contents.match(/<main\b[^>]*>[\s\S]*?<\/main>/iu);
  return main ? main[0] : "";
}

const forbiddenContent = [
  /(?:GH_TOKEN|DEVHUD_[A-Z0-9_]*SECRET|Authorization:\s*Bearer)/iu,
  /(?:\/home\/|\/Users\/|[A-Z]:\\|~\/\.config\/)/u,
  /(?:private key|signing key|password|access key|secret)\s*[:=]\s*[^\s<`]+/iu,
  /partial\s+(?:GA|general availability)|staged\s+general availability/iu,
];

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

  for (const pattern of forbiddenContent) {
    if (pattern.test(contents)) {
      failures.push(`${path.relative(outputDir, htmlFile)} contains prohibited public content`);
    }
  }
}

for (const [routeId, headings] of requiredHeadings) {
  const route = routeOutputFiles.find((entry) => entry.routeId === routeId);
  const contents = route ? await readFile(route.outputFile, "utf8") : "";
  for (const heading of headings) {
    if (!contents.includes(heading)) failures.push(`${routeId} is missing required heading text: ${heading}`);
  }
  if (!/<main\b[^>]*>/iu.test(contents)) failures.push(`${routeId} is missing a main landmark`);
}

for (const [routeId, links] of requiredLinks) {
  const route = routeOutputFiles.find((entry) => entry.routeId === routeId);
  const contents = route
    ? articleContent(await readFile(route.outputFile, "utf8"))
    : "";
  for (const link of links) {
    if (!contents.includes(`href="${link}"`) && !contents.includes(`href='${link}'`)) {
      failures.push(`${routeId} is missing required link ${link}`);
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
