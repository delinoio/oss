import { access, readFile } from "node:fs/promises";
import path from "node:path";

const outputDirectory = path.resolve("doc_build");
const retiredOrigins = [
  "https://runmoor.delino.io",
  "https://nodeup.delino.io",
  "https://binpm.delino.io",
  "https://ach.delino.io",
];
const projectRoutes = {
  runmoor: ["/", "/install", "/configuration", "/commands", "/docker", "/tart", "/operations"],
  nodeup: ["/", "/installation", "/getting-started", "/commands", "/runtime-resolution", "/shims-and-package-managers", "/output", "/completions", "/releases", "/troubleshooting", "/reference"],
  binpm: ["/", "/installation", "/getting-started", "/commands", "/local-tooling", "/cache-and-verification", "/releases", "/troubleshooting", "/reference"],
  "async-commit-hook": ["/", "/install", "/start", "/configuration", "/validation", "/commands", "/agents", "/web", "/privacy", "/recovery", "/compatibility", "/symlinks", "/existing-hooks"],
};
const selectorDestinations = ["/", "/runmoor/", "/nodeup/", "/binpm/", "/async-commit-hook/"];
const failures = [];

async function exists(filePath) {
  try {
    await access(filePath);
    return true;
  } catch {
    return false;
  }
}

function artifactPath(slug, route) {
  return path.join(outputDirectory, slug, route === "/" ? "index.html" : `${route.slice(1)}.html`);
}

function publicRoute(slug, route) {
  return route === "/" ? `/${slug}/` : `/${slug}${route}`;
}

function collectSameOriginLinks(contents) {
  return [...contents.matchAll(/\b(?:href|src|srcset|poster|action|formaction|data)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'`=<>]+))/giu)]
    .map((match) => match[1] ?? match[2] ?? match[3] ?? "")
    .flatMap((value) => value.split(",").map((candidate) => candidate.trim().split(/\s+/u, 1)[0]))
    .filter((value) => value.startsWith("/"));
}

for (const [slug, routes] of Object.entries(projectRoutes)) {
  const projectRoot = path.join(outputDirectory, slug);
  if (!(await exists(projectRoot))) {
    failures.push(`${slug} output directory is missing`);
    continue;
  }

  for (const route of routes) {
    const file = artifactPath(slug, route);
    if (!(await exists(file))) {
      failures.push(`${publicRoute(slug, route)} is missing its HTML artifact`);
      continue;
    }
    const contents = await readFile(file, "utf8");
    if (!/<main\b/iu.test(contents)) failures.push(`${publicRoute(slug, route)} is missing a main landmark`);
    if (!contents.includes("delino-docs-site-switcher")) failures.push(`${publicRoute(slug, route)} is missing the site selector`);
    for (const destination of selectorDestinations) {
      if (!contents.includes(`href="${destination}"`) && !contents.includes(`href='${destination}'`)) {
        failures.push(`${publicRoute(slug, route)} is missing selector destination ${destination}`);
      }
    }
    const selected = (contents.match(/aria-current="page"/gu) ?? []).length;
    if (selected !== 1) failures.push(`${publicRoute(slug, route)} has ${selected} selected site destinations`);
    for (const oldOrigin of retiredOrigins) {
      if (contents.includes(oldOrigin)) failures.push(`${publicRoute(slug, route)} contains retired origin ${oldOrigin}`);
    }
    for (const link of collectSameOriginLinks(contents)) {
      if (/\.html(?:[?#]|$)/iu.test(link)) failures.push(`${publicRoute(slug, route)} contains an .html link: ${link}`);
      if (link.startsWith(`/${slug}/`) || link === `/${slug}`) continue;
      if (selectorDestinations.includes(link)) continue;
      if (/^\/(?:assets|static)\//u.test(link)) continue;
      if (link.startsWith("#")) continue;
      if (link.startsWith("/")) failures.push(`${publicRoute(slug, route)} escapes its project subpath: ${link}`);
    }
  }
}

const installerChecks = [
  ["nodeup", "install.sh", "scripts/install/nodeup.sh"],
  ["nodeup", "install.ps1", "scripts/install/nodeup.ps1"],
  ["binpm", "install.sh", "scripts/install/binpm.sh"],
  ["binpm", "install.ps1", "scripts/install/binpm.ps1"],
  ["async-commit-hook", "install.sh", "apps/async-commit-hook-docs/public/install.sh"],
  ["async-commit-hook", "install.ps1", "apps/async-commit-hook-docs/public/install.ps1"],
];
for (const [slug, filename, source] of installerChecks) {
  const generated = path.join(outputDirectory, slug, filename);
  const sourceFile = path.resolve("../..", source);
  if (!(await exists(generated))) {
    failures.push(`/${slug}/${filename} is missing`);
    continue;
  }
  const [generatedContents, sourceContents] = await Promise.all([readFile(generated), readFile(sourceFile)]);
  if (!generatedContents.equals(sourceContents)) failures.push(`/${slug}/${filename} differs from ${source}`);
}

for (const file of [
  path.join(outputDirectory, "async-commit-hook", "docs", "index.html"),
  path.join(outputDirectory, "async-commit-hook", "docs", "redirect.js"),
]) {
  if (!(await exists(file))) failures.push(`${path.relative(outputDirectory, file)} is missing`);
}
const redirects = await readFile(path.join(outputDirectory, "_redirects"), "utf8").catch(() => "");
if (redirects !== "/async-commit-hook/docs /async-commit-hook/docs/ 301\n") failures.push("aggregate async /docs redirect is missing or incorrect");
const headers = await readFile(path.join(outputDirectory, "_headers"), "utf8").catch(() => "");
const expectedAsyncHeaders = `/async-commit-hook/*
  Referrer-Policy: no-referrer
  X-Content-Type-Options: nosniff
  X-Frame-Options: DENY
  Permissions-Policy: camera=(), microphone=(), geolocation=()
`;
if (headers !== expectedAsyncHeaders) failures.push("aggregate async security headers are missing or incorrect");

if (failures.length > 0) {
  console.error("Integrated public docs validation failed:");
  for (const failure of failures) console.error(`- ${failure}`);
  process.exit(1);
}

console.log("Integrated public docs validation passed.");
