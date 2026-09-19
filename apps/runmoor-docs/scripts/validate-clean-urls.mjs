import { readdir, readFile } from "node:fs/promises";
import path from "node:path";
import { decodeHTML } from "entities";
import { createPublicContentValidator, resourceTargets } from "./public-content.mjs";
import { containsAffirmativeReleaseClaim } from "./release-claims.mjs";

const requiredHeadings = new Map([
  ["/", ["Runmoor", "Guides"]],
  ["/install", ["Install and Verify Runmoor"]],
  ["/configuration", ["Runmoor Configuration"]],
  ["/commands", ["Runmoor Commands and Routing", "Complete CLI reference"]],
  ["/docker", ["Runmoor Docker Execution"]],
  ["/tart", ["Runmoor Tart Images"]],
  ["/operations", ["Runmoor Operations", "Troubleshooting and privacy"]],
]);
const requiredLinks = new Map([
  ["/", [...requiredHeadings.keys()].filter((route) => route !== "/")],
  ["/configuration", ["/docker", "/tart"]],
  ["/commands", ["/tart", "/operations"]],
  ["/operations", ["/install"]],
]);
const outputDir = path.resolve("doc_build");
const validatorOrigin = "https://runmoor.delino.io";
const failures = [];
const containsProhibitedPublicContent = createPublicContentValidator(requiredHeadings.keys());

async function collectPublicationFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const filename = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...await collectPublicationFiles(filename));
    else if (/\.(?:html|css)$/u.test(entry.name)) files.push(filename);
  }
  return files;
}

function resolveLinks(targets, pageUrl) {
  return targets.map((target) => decodeHTML(target))
    .filter((href) => !href.startsWith("#"))
    .flatMap((href) => {
      try {
        return [new URL(href, pageUrl)];
      } catch {
        // URL errors retain the original input, which can contain credentials.
        failures.push(`${pageUrl.pathname} contains a malformed link`);
        return [];
      }
    });
}

function links(contents, pageUrl) {
  return resolveLinks([...contents.matchAll(/<a\b[^>]*\bhref\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'`=<>]+))/giu)]
    .map((match) => match[1] ?? match[2] ?? match[3]), pageUrl);
}

function articleContent(contents) {
  return contents.match(/<article\b[^>]*>[\s\S]*?<\/article>/iu)?.[0]
    ?? contents.match(/<main\b[^>]*>[\s\S]*?<\/main>/iu)?.[0]
    ?? "";
}

function articleHeadings(contents) {
  return new Set([...contents.matchAll(/<h[1-6]\b[^>]*>([\s\S]*?)<\/h[1-6]>/giu)]
    .map(([, heading]) => decodeHTML(heading
      .replace(/<a\b[^>]*aria-hidden="true"[^>]*>[\s\S]*?<\/a>/giu, "")
      .replace(/<[^>]*>/gu, " "))
      .replace(/\s+/gu, " ").trim()));
}

function classRegions(contents, tag, className) {
  const regions = [];
  const stack = [];
  // Rspress nests divs and lists within its discovery regions. Balance matching
  // tags so a descendant's closing tag cannot truncate the inspected region.
  for (const match of contents.matchAll(new RegExp(`</?${tag}\\b[^>]*>`, "giu"))) {
    if (match[0].startsWith("</")) {
      const opening = stack.pop();
      if (opening?.selected) regions.push(contents.slice(opening.start, match.index + match[0].length));
    } else if (!match[0].endsWith("/>")) {
      const classes = match[0].match(/\bclass\s*=\s*(?:"([^"]*)"|'([^']*)')/iu);
      stack.push({
        start: match.index,
        selected: decodeHTML(classes?.[1] ?? classes?.[2] ?? "").split(/\s+/u).includes(className),
      });
    }
  }
  return regions;
}

const publicationFiles = await collectPublicationFiles(outputDir);
const contentsByFile = new Map(await Promise.all(publicationFiles.map(async (file) => [file, await readFile(file, "utf8")])));

for (const [file, contents] of contentsByFile) {
  const relativeFile = path.relative(outputDir, file).split(path.sep).join("/");
  const pageUrl = new URL(`/${relativeFile}`, validatorOrigin);
  const stylesheet = file.endsWith(".css");
  if (containsProhibitedPublicContent(contents, pageUrl, { stylesheet })) {
    // Never echo the rejected content or URL into public CI logs.
    failures.push(`${relativeFile} contains prohibited public content`);
    continue;
  }
  for (const link of resolveLinks(resourceTargets(contents), pageUrl)) {
    if (link.origin !== validatorOrigin) continue;
    if (/^\/runmoor(?:\/|(?:\.html)?$)/u.test(link.pathname)) {
      failures.push(`${relativeFile} links to legacy route ${link.pathname}`);
    } else if (link.pathname.endsWith(".html")) {
      failures.push(`${relativeFile} links to non-clean route ${link.pathname}`);
    }
  }
  if (stylesheet) continue;
  const renderedText = decodeHTML(contents
    .replace(/<(?:script|style)\b[^>]*>[\s\S]*?<\/(?:script|style)>/giu, " ")
    .replace(/<[^>]*>/gu, " ")).replace(/\s+/gu, " ");
  if (containsAffirmativeReleaseClaim(renderedText)) {
    failures.push(`${relativeFile} contains an unsupported release claim`);
  }
  if (/^runmoor(?:\/|\.html$)/u.test(relativeFile)) {
    failures.push(`${relativeFile} retains a legacy route artifact`);
  }
  for (const link of links(contents, pageUrl)) {
    if (link.origin !== validatorOrigin) continue;
    if (!requiredHeadings.has(link.pathname)) {
      failures.push(`${relativeFile} links to unknown route ${link.pathname}`);
    }
  }
}

for (const [route, headings] of requiredHeadings) {
  const outputFile = path.join(outputDir, route === "/" ? "index.html" : `${route.slice(1)}.html`);
  const contents = contentsByFile.get(outputFile);
  if (!contents) {
    failures.push(`${route} is missing its output artifact`);
    continue;
  }
  if (!/<main\b/iu.test(contents)) failures.push(`${route} is missing a main landmark`);
  const article = articleContent(contents);
  const actualHeadings = articleHeadings(article);
  for (const heading of headings) {
    if (!actualHeadings.has(heading)) failures.push(`${route} is missing heading: ${heading}`);
  }
  const articleLinks = new Set(links(article, new URL(route, validatorOrigin))
    .filter((link) => link.origin === validatorOrigin).map((link) => link.pathname));
  for (const link of requiredLinks.get(route) ?? []) {
    if (!articleLinks.has(link)) failures.push(`${route} is missing article link ${link}`);
  }
  const pageUrl = new URL(route, validatorOrigin);
  const header = classRegions(contents, "header", "rp-nav").join("");
  for (const [name, regions] of [
    ["top navigation", classRegions(header, "ul", "rp-nav-menu")],
    ["sidebar", classRegions(contents, "aside", "rp-doc-layout__sidebar")],
  ]) {
    const regionLinks = new Set(links(regions.join(""), pageUrl)
      .filter((link) => link.origin === validatorOrigin).map((link) => link.pathname));
    for (const destination of requiredHeadings.keys()) {
      if (!regionLinks.has(destination)) failures.push(`${route} is missing ${name} link ${destination}`);
    }
  }
  const socialRegions = classRegions(header, "div", "rp-social-links");
  if (socialRegions.length === 0 || socialRegions.some((region) => !links(region, pageUrl)
    .some((link) => link.href === "https://github.com/delinoio/oss"))) {
    failures.push(`${route} is missing the social navigation repository link`);
  }
  const footer = contents.match(/<footer\b[^>]*class="delino-repository-footer"[^>]*>[\s\S]*?<\/footer>/iu)?.[0] ?? "";
  if (!links(footer, new URL(route, validatorOrigin))
    .some((link) => link.href === "https://github.com/delinoio/oss")) {
    failures.push(`${route} is missing the document footer repository link`);
  }
}

if (failures.length > 0) {
  console.error("Runmoor docs clean URL validation failed:");
  for (const failure of failures) console.error(`- ${failure}`);
  process.exit(1);
}

console.log("Runmoor docs clean URL validation passed (7 routes).");
