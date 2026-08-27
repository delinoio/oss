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
const stableRoutePathPattern = stableRouteIds
  .filter((routeId) => routeId !== "/")
  .sort((left, right) => right.length - left.length)
  .map((routeId) => routeId.slice(1).replace(/[.*+?^${}()|[\]\\]/gu, "\\$&"))
  .join("|");
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
      ? /href=(["'])(?:(?:\.\.?\/)*|\/)index\.html(?:[?#][^"']*)?\1/
      : new RegExp(
          `href=(["'])(?:(?:\\.\\.?\\/)*|\\/)${routeId.slice(1)}\\.html(?:[?#][^"']*)?\\1`,
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

function articleHeadings(contents) {
  return [...contents.matchAll(/<h[1-6]\b[^>]*>([\s\S]*?)<\/h[1-6]>/giu)].map(
    ([, heading]) => heading
      .replace(/<a\b[^>]*aria-hidden="true"[^>]*>[\s\S]*?<\/a>/giu, "")
      .replace(/<[^>]*>/gu, " ")
      .replace(/\s+/gu, " ")
      .trim(),
  );
}

function decodeHtmlEntities(contents) {
  return contents
    .replace(/&#(\d+);/gu, (_, value) => String.fromCodePoint(Number(value)))
    .replace(/&#x([\da-f]+);/giu, (_, value) => String.fromCodePoint(Number.parseInt(value, 16)))
    .replace(/&(?:amp|lt|gt|quot|apos|nbsp);/giu, (entity) => ({
      "&amp;": "&",
      "&lt;": "<",
      "&gt;": ">",
      "&quot;": '"',
      "&apos;": "'",
      "&nbsp;": " ",
    })[entity.toLowerCase()] ?? entity);
}

function visibleText(contents) {
  return decodeHtmlEntities(contents
    .replace(/<(?:script|style)\b[^>]*>[\s\S]*?<\/(?:script|style)>/giu, " ")
    .replace(/<[^>]*>/gu, " "))
    .replace(/\s+/gu, " ");
}

const forbiddenContent = [
  /(?:GH_TOKEN|DEVHUD_[A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|KEY)|Authorization:\s*Bearer)/iu,
  /(?:token|access[_-]?token|refresh[_-]?token|api[_-]?key)\s*[:=]\s*[^\s<`]+/iu,
  /(?:private key|signing key|password|access key|secret)\s*[:=]\s*[^\s<`]+/iu,
];
const forbiddenPathContent = [
  new RegExp(`(?:^|[\\s("'\\x60>])/(?!${stableRoutePathPattern}(?:\\.html)?(?:[/?#"'\\x60<\\s]|$))[A-Za-z0-9._~-]+(?:[/\\\\][^\\s"'\\x60<>]*)?`, "u"),
  /(?:^|[\s("'`>])[A-Za-z]:[\\/][^\s"'`<>]*/u,
  /(?:^|[\s("'`>])\\\\[^\s"'`<>\\/]+[\\/][^\s"'`<>]*/u,
];
const releaseAvailabilityClaim =
  /\b(?:partial|staged)\s+(?:GA|general[- ]availability)\b|\b(?:partial|staged)\s+or\s+(?:staged|partial)\s+general[- ]availability\b/giu;
const negativeAvailabilityPredicate =
  /^\s*(?:(?:is|are|remains?|remain)\s+(?:(?:not|never)\s+)?(?:available|supported|permitted|allowed|prohibited|forbidden|disallowed|excluded|unsupported)|will\s+not\s+(?:be\s+)?(?:available|supported|permitted|allowed|prohibited|forbidden|disallowed|excluded|unsupported))\b/iu;

function containsAffirmativeReleaseClaim(contents) {
  for (const match of contents.matchAll(releaseAvailabilityClaim)) {
    const sentenceStart = Math.max(
      contents.lastIndexOf(".", match.index) + 1,
      contents.lastIndexOf("!", match.index) + 1,
      contents.lastIndexOf("?", match.index) + 1,
    );
    const sentenceEndOffset = contents
      .slice(match.index + match[0].length)
      .search(/[.!?]/u);
    const sentenceEnd =
      sentenceEndOffset === -1
        ? -1
        : match.index + match[0].length + sentenceEndOffset;
    const sentence = contents.slice(sentenceStart, sentenceEnd === -1 ? contents.length : sentenceEnd);
    const phraseOffset = match.index - sentenceStart;
    const prefix = sentence.slice(0, phraseOffset);
    const suffix = sentence.slice(phraseOffset + match[0].length);
    if (/\b(?:no|not|never|without)\s*$/iu.test(prefix)) continue;
    if (negativeAvailabilityPredicate.test(suffix)) continue;
    return true;
  }
  return false;
}

for (const { routeId, outputFile } of routeOutputFiles) {
  if (!(await pathExists(outputFile))) {
    failures.push(
      `${routeId} was not emitted at ${path.relative(outputDir, outputFile)}`,
    );
  }
}

for (const htmlFile of htmlFiles) {
  const contents = await readFile(htmlFile, "utf8");
  const renderedText = visibleText(contents);

  for (const { htmlRoute, pattern } of htmlHrefPatterns) {
    if (pattern.test(contents)) {
      failures.push(`${path.relative(outputDir, htmlFile)} links to ${htmlRoute}`);
    }
  }

  for (const pattern of forbiddenContent) {
    if (pattern.test(contents) || pattern.test(renderedText)) {
      failures.push(`${path.relative(outputDir, htmlFile)} contains prohibited public content`);
    }
  }
  for (const pattern of forbiddenPathContent) {
    if (pattern.test(renderedText)) {
      failures.push(`${path.relative(outputDir, htmlFile)} contains prohibited public content`);
    }
  }
  if (containsAffirmativeReleaseClaim(renderedText)) {
    failures.push(`${path.relative(outputDir, htmlFile)} contains prohibited public content`);
  }
}

for (const [routeId, headings] of requiredHeadings) {
  const route = routeOutputFiles.find((entry) => entry.routeId === routeId);
  const pageContents = route ? await readFile(route.outputFile, "utf8") : "";
  const contents = articleContent(pageContents);
  const headingsInArticle = new Set(articleHeadings(contents));
  for (const heading of headings) {
    if (!headingsInArticle.has(heading)) failures.push(`${routeId} is missing required heading text: ${heading}`);
  }
  if (!/<main\b[^>]*>/iu.test(pageContents)) failures.push(`${routeId} is missing a main landmark`);
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
