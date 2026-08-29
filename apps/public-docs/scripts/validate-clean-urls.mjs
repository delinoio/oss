import { access, readdir, readFile, stat } from "node:fs/promises";
import path from "node:path";
import { decodeHTML } from "entities";

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
const htmlRoutePaths = new Set(
  stableRouteIds.map((routeId) => (routeId === "/" ? "/index.html" : `${routeId}.html`)),
);
const urlAttributePattern = /\b(?:href|src|srcset|poster|action|formaction|data)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'`=<>]+))/giu;
const anchorHrefPattern = /<a\b[^>]*\bhref\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'`=<>]+))/giu;
const validatorOrigin = "https://public-docs.invalid";

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

function attributeValue(match) {
  return match[1] ?? match[2] ?? match[3] ?? "";
}

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

function visibleText(contents) {
  return decodeHTML(contents
    .replace(/<(?:script|style)\b[^>]*>[\s\S]*?<\/(?:script|style)>/giu, " ")
    .replace(/<[^>]*>/gu, " "))
    .replace(/\s+/gu, " ");
}

function findHtmlRouteLinks(contents, htmlFile) {
  const pagePath = `/${path.relative(outputDir, htmlFile).split(path.sep).join("/")}`;
  const pageUrl = new URL(pagePath, validatorOrigin);
  const invalidRoutes = [];

  for (const match of contents.matchAll(anchorHrefPattern)) {
    const href = attributeValue(match);
    const decodedHref = decodeHTML(href);
    if (!/\.html(?:[?#]|$)/iu.test(decodedHref)) continue;
    let resolvedUrl;
    try {
      resolvedUrl = new URL(decodedHref, pageUrl);
    } catch {
      continue;
    }
    if (resolvedUrl.origin === validatorOrigin && htmlRoutePaths.has(resolvedUrl.pathname)) {
      invalidRoutes.push(resolvedUrl.pathname);
    }
  }

  return invalidRoutes;
}

function containsCredentialBearingLink(contents, htmlFile) {
  const pagePath = `/${path.relative(outputDir, htmlFile).split(path.sep).join("/")}`;
  const pageUrl = new URL(pagePath, validatorOrigin);

  for (const match of contents.matchAll(urlAttributePattern)) {
    const href = attributeValue(match);
    try {
      const resolvedUrl = new URL(decodeHTML(href), pageUrl);
      if (resolvedUrl.username || resolvedUrl.password || containsCredentialBearingQuery(resolvedUrl)) return true;
    } catch {
      // Invalid URLs are handled by the browser/build output and are not credential evidence.
    }
  }

  return false;
}

function normalizedUrlPath(target, htmlFile) {
  const pagePath = `/${path.relative(outputDir, htmlFile).split(path.sep).join("/")}`;
  try {
    const resolvedUrl = new URL(decodeHTML(target), new URL(pagePath, validatorOrigin));
    if (resolvedUrl.origin !== validatorOrigin) return null;
    if (htmlRoutePaths.has(resolvedUrl.pathname) || /^(?:\/assets|\/static)\//u.test(resolvedUrl.pathname)) return null;
    return decodeURIComponent(resolvedUrl.pathname);
  } catch {
    return null;
  }
}

function hrefPathTargets(contents, htmlFile) {
  const targets = [];
  for (const match of contents.matchAll(urlAttributePattern)) {
    const target = decodeHTML(attributeValue(match));
    const normalizedPath = normalizedUrlPath(target, htmlFile);
    if (normalizedPath || !/^(?:\/assets|\/static)\//u.test(target)) targets.push(target);
    if (normalizedPath) targets.push(normalizedPath);
  }
  return targets;
}

const credentialQueryKey = /^(?:token|access[_-]?token|refresh[_-]?token|api[_-]?key|password|secret)$/iu;

function containsCredentialBearingQuery(url) {
  return containsCredentialBearingParameters(url.searchParams);
}

function containsCredentialBearingFragment(url) {
  if (!url.hash) return false;
  const fragment = url.hash.slice(1).replace(/^\?/u, "");
  return containsCredentialBearingParameters(new URLSearchParams(fragment));
}

function containsCredentialBearingParameters(parameters) {
  for (const [key, value] of parameters) {
    const pair = `${key}=${value}`;
    if (credentialQueryKey.test(key) || forbiddenContent.some((pattern) => pattern.test(pair))) return true;
  }
  return false;
}

function resourceTargets(contents) {
  const targets = [];
  for (const match of contents.matchAll(urlAttributePattern)) {
    const value = attributeValue(match);
    if (/\bsrcset\s*=/iu.test(match[0]) && value.includes(",")) {
      for (const candidate of value.split(",")) {
        const target = candidate.trim().split(/\s+/u, 1)[0];
        if (target) targets.push(target);
      }
    } else {
      targets.push(value.trim());
    }
  }
  return targets;
}

function isApprovedPublicAsset(target, htmlFile) {
  try {
    const pagePath = `/${path.relative(outputDir, htmlFile).split(path.sep).join("/")}`;
    const resolvedUrl = new URL(decodeHTML(target), new URL(pagePath, validatorOrigin));
    return resolvedUrl.origin === validatorOrigin
      && (/^\/(?:assets|static)\//u.test(resolvedUrl.pathname));
  } catch {
    return false;
  }
}

function containsForbiddenResourcePath(contents, htmlFile) {
  const resourceTargetsToCheck = resourceTargets(contents)
    .filter((target) => !isApprovedPublicAsset(target, htmlFile));
  return forbiddenPathContent.some((pattern) => resourceTargetsToCheck.some((target) => {
    const decodedTarget = decodeHTML(target);
    const normalizedPath = normalizedUrlPath(target, htmlFile);
    return pattern.test(decodedTarget) || (normalizedPath !== null && pattern.test(normalizedPath));
  }));
}

function containsCredentialBearingResource(contents, htmlFile) {
  const pagePath = `/${path.relative(outputDir, htmlFile).split(path.sep).join("/")}`;
  const pageUrl = new URL(pagePath, validatorOrigin);

  for (const target of resourceTargets(contents)) {
    try {
      const resolvedUrl = new URL(decodeHTML(target), pageUrl);
      if (
        resolvedUrl.username
        || resolvedUrl.password
        || containsCredentialBearingQuery(resolvedUrl)
        || containsCredentialBearingFragment(resolvedUrl)
      ) return true;
    } catch {
      // Invalid URLs are handled by the browser/build output and are not credential evidence.
    }
  }

  return false;
}

const forbiddenContent = [
  /(?:GH_TOKEN|DEVHUD_[A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|KEY)|Authorization:\s*Bearer)/iu,
  /(?:"(?:token|access[_-]?token|refresh[_-]?token|api[_-]?key)"|(?:token|access[_-]?token|refresh[_-]?token|api[_-]?key))\s*[:=]\s*[^\s<`]+/iu,
  /(?:"(?:private key|signing key|password|access key|secret)"|(?:private key|signing key|password|access key|secret))\s*[:=]\s*[^\s<`]+/iu,
  /\b(?:ghp|github_pat)_[A-Za-z0-9_]+\b/iu,
];
const forbiddenContentFixtures = [
  [
    "json",
    '```json\n{"refresh_token":"abc123","password":"not-a-real-secret"}\n```',
  ],
  ["classic GitHub PAT", "ghp_1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJ"],
  ["fine-grained GitHub PAT", "github_pat_11ABCDEFGHijklmnopQRSTUVwxyz0123456789"],
];
const forbiddenPathContent = [
  new RegExp(`(?:^|[\\s("'\\x60>])/(?!${stableRoutePathPattern}(?:\\.html)?(?:[?#"'\\x60<\\s]|$))[A-Za-z0-9._~-]+(?:[/\\\\][^\\s"'\\x60<>]*)?`, "u"),
  /(?:^|[\s("'`>])(?:\.\.[\\/])+(?:[A-Za-z0-9._~-]+[\\/])+[^\s"'`<>]*/u,
  /(?:^|[\s("'`>])(?:apps|cmds|crates|docs|packaging|packages|protos|scripts|servers)(?:[\\/][^\s"'`<>]+)+/u,
  /(?:^|[\s("'`>])[A-Za-z]:[\\/][^\s"'`<>]*/u,
  /(?:^|[\s("'`>])\\\\[^\s"'`<>\\/]+[\\/][^\s"'`<>]*/u,
  /(?:^|[\s("'`>])file:\/\/(?:[^\/\s"'`<>]+)?\/[^\s"'`<>]*/iu,
];
const forbiddenLinkFixtures = [
  '<a href="https://alice:secret@example.com/support">support</a>',
  '<a href="https://example.com/?token&equals;abc123">support</a>',
];
const invalidRouteAttributeFixtures = [
  '<a href = "/devhud/install.html">install</a>',
  "<a href=/devhud/install.html>install</a>",
  '<a href="/devhud/install&#46;html">install</a>',
];
const externalRouteFixtures = [
  '<a href="https://docs.example.com/devhud/install.html">install</a>',
];
const forbiddenPathFixtures = [
  '<a href="file://server/share/private.conf">private file</a>',
  '<a href="file://localhost/etc/private.conf">private file</a>',
  '<a href="../../servers/devhud/config">private repository path</a>',
  "repository path: servers/devhud/config",
];
const encodedForbiddenPathFixtures = [
  '<img src="%2E%2E/%2E%2E/servers/devhud/private.png">',
];
const forbiddenResourceFixtures = [
  '<img src="file:///etc/devhud/private.png">',
  '<img srcset="../../servers/devhud/private.png 1x">',
  '<img src = "file:///etc/devhud/private.png">',
  '<img src=file:///etc/devhud/private.png>',
  '<img src="file&colon;///etc/devhud/private.png">',
  '<video poster="file:///etc/devhud/private.png"></video>',
  '<form action="../../servers/devhud/private-endpoint"></form>',
  '<button formaction="file:///etc/devhud/private-endpoint">Submit</button>',
  '<object data="../../servers/devhud/private.pdf"></object>',
];
const credentialBearingResourceFixtures = [
  '<img src="https://alice:secret@example.com/image.png">',
  '<img src = https://alice:secret@example.com/image.png>',
  '<img src="https://example.com/image?token&equals;abc123">',
  '<img src="https://example.com/image#token&equals;abc123">',
  '<video poster="https://alice:secret@example.com/image.png"></video>',
  '<form action="https://example.com/submit?token&equals;abc123"></form>',
  '<button formaction="https://example.com/submit#token&equals;abc123">Submit</button>',
  '<object data="https://alice:secret@example.com/document.pdf"></object>',
];
const hrefAttributeFixtures = [
  '<link rel="stylesheet" href="https://alice:secret@example.com/x.css">',
  '<area href="https://example.com/image?token&equals;abc123">',
];
const approvedResourceFixtures = [
  '<img src="/assets/logo.svg">',
  '<script src="/static/js/app.js"></script>',
];
const releaseAvailabilityClaim =
  /\b(?:partial|staged)\s+(?:GA|availability|general[- ]availability)\b|\b(?:partial|staged)\s+or\s+(?:staged|partial)\s+general[- ]availability\b|\b(?:beta|phased|fractional)\s+(?:GA|availability|general[- ]availability|rollout|channel)\b|\bearly[- ]access(?:\s+(?:GA|availability|general[- ]availability|rollout|channel))?\b|\bearly announcement\b/giu;
const negativeAvailabilityPredicate =
  /^\s*(?:(?:is|are|remains?|remain)\s+(?:(?:not|never)\s+(?:available|supported|permitted|allowed|excluded|unsupported)|(?:unavailable|unsupported|excluded))|isn't\s+(?:available|supported|permitted|allowed|excluded|unsupported)|will\s+not\s+(?:be\s+)?(?:available|supported|permitted|allowed|excluded|unsupported))\b/iu;
const unnegatedProhibitionPredicate =
  /^\s*(?:is|are|remains?|remain)\s+(?:prohibited|forbidden|disallowed)\b/iu;

const releaseAvailabilityFixtures = [
  ["partial GA is not prohibited", true],
  ["staged availability is never forbidden", true],
  ["partial GA is not available", false],
  ["partial GA is prohibited", false],
  ["beta channel is available", true],
  ["phased rollout is supported", true],
  ["fractional rollout is permitted", true],
  ["early-access channel is allowed", true],
  ["early announcement is available", true],
  ["beta channel is not available", false],
  ["beta channel is unsupported", false],
  ["beta channel is unavailable", false],
  ["beta channel isn't available", false],
  ["early announcement is not available", false],
];

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
    if (
      negativeAvailabilityPredicate.test(suffix)
      || unnegatedProhibitionPredicate.test(suffix)
    ) continue;
    return true;
  }
  return false;
}

for (const [fixture, expected] of releaseAvailabilityFixtures) {
  if (containsAffirmativeReleaseClaim(fixture) !== expected) {
    failures.push(`release availability fixture was classified incorrectly: ${fixture}`);
  }
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

  for (const htmlRoute of findHtmlRouteLinks(contents, htmlFile)) {
    failures.push(`${path.relative(outputDir, htmlFile)} links to ${htmlRoute}`);
  }

  for (const pattern of forbiddenContent) {
    if (pattern.test(contents) || pattern.test(renderedText)) {
      failures.push(`${path.relative(outputDir, htmlFile)} contains prohibited public content`);
    }
  }
  if (containsCredentialBearingLink(contents, htmlFile)) {
    failures.push(`${path.relative(outputDir, htmlFile)} contains prohibited public content`);
  }
  if (containsForbiddenResourcePath(contents, htmlFile)) {
    failures.push(`${path.relative(outputDir, htmlFile)} contains prohibited public content`);
  }
  if (containsCredentialBearingResource(contents, htmlFile)) {
    failures.push(`${path.relative(outputDir, htmlFile)} contains prohibited public content`);
  }
  for (const pattern of forbiddenPathContent) {
    const hrefTargets = hrefPathTargets(contents, htmlFile);
    if (pattern.test(renderedText) || hrefTargets.some((target) => pattern.test(target))) {
      failures.push(`${path.relative(outputDir, htmlFile)} contains prohibited public content`);
    }
  }
  if (containsAffirmativeReleaseClaim(renderedText)) {
    failures.push(`${path.relative(outputDir, htmlFile)} contains prohibited public content`);
  }
}

for (const [language, fixture] of forbiddenContentFixtures) {
  if (!forbiddenContent.some((pattern) => pattern.test(fixture))) {
    failures.push(`${language} credential fixture was not detected`);
  }
}

for (const fixture of forbiddenLinkFixtures) {
  if (!containsCredentialBearingLink(fixture, path.join(outputDir, "fixture.html"))) {
    failures.push("credential-bearing link fixture was not detected");
  }
}

for (const fixture of hrefAttributeFixtures) {
  if (!containsCredentialBearingLink(fixture, path.join(outputDir, "fixture.html"))) {
    failures.push("credential-bearing href attribute fixture was not detected");
  }
}

for (const fixture of invalidRouteAttributeFixtures) {
  if (findHtmlRouteLinks(fixture, path.join(outputDir, "fixture.html")).length === 0) {
    failures.push("invalid HTML route attribute fixture was not detected");
  }
}

for (const fixture of externalRouteFixtures) {
  if (findHtmlRouteLinks(fixture, path.join(outputDir, "fixture.html")).length !== 0) {
    failures.push("external HTML route fixture was incorrectly rejected");
  }
}

for (const fixture of forbiddenPathFixtures) {
  if (!forbiddenPathContent.some((pattern) => pattern.test(fixture))) {
    failures.push("forbidden filesystem path fixture was not detected");
  }
}

for (const fixture of encodedForbiddenPathFixtures) {
  if (!containsForbiddenResourcePath(fixture, path.join(outputDir, "fixture.html"))) {
    failures.push("encoded forbidden resource path fixture was not detected");
  }
}

for (const fixture of ['<link rel="stylesheet" href="file:///etc/private.css">']) {
  if (!forbiddenPathContent.some((pattern) => hrefPathTargets(fixture, path.join(outputDir, "fixture.html")).some((target) => pattern.test(target)))) {
    failures.push("forbidden non-anchor href path fixture was not detected");
  }
}

for (const fixture of forbiddenResourceFixtures) {
  if (!containsForbiddenResourcePath(fixture, path.join(outputDir, "fixture.html"))) {
    failures.push("forbidden resource path fixture was not detected");
  }
}

for (const fixture of credentialBearingResourceFixtures) {
  if (!containsCredentialBearingResource(fixture, path.join(outputDir, "fixture.html"))) {
    failures.push("credential-bearing resource fixture was not detected");
  }
}

for (const fixture of approvedResourceFixtures) {
  if (containsForbiddenResourcePath(fixture, path.join(outputDir, "fixture.html"))) {
    failures.push("approved public resource fixture was incorrectly rejected");
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
