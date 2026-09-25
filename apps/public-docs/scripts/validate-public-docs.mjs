import { access, readFile } from "node:fs/promises";
import path from "node:path";
import { projectRoutes } from "./project-routes.mjs";

const outputDirectory = path.resolve("doc_build");
const retiredOrigins = [
  "https://runmoor.delino.io",
  "https://nodeup.delino.io",
  "https://binpm.delino.io",
  "https://ach.delino.io",
];

const selectorDestinations = ["/", "/runmoor/", "/nodeup/", "/binpm/", "/async-commit-hook/", "/clibox/", "/pnport/", "/react-forge/"];
const projectSecuritySlugs = new Set(["runmoor", "async-commit-hook", "pnport", "react-forge"]);
const forbiddenProjectContent = [
  /(?:GH_TOKEN|DEVHUD_[A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|KEY)|Authorization:\s*Bearer)/iu,
  /\b(?:ghp|github_pat)_[A-Za-z0-9_]+\b/iu,
  /BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY/iu,
  /(?:\/Users\/|\/home\/[a-z]|\.infisical)/iu,
  /(?:apps|cmds|servers|protos)\/(?:runmoor|async-commit-hook|devhud)(?:\/|\b)/iu,
  /(?:crates|packages)\/pnport(?:\/|\b)/iu,
  /(?:crates|packages)\/react-forge(?:\/|\b)/iu,
];
const rootRoutes = [
  "/",
  "/getting-started",
  "/projects-overview",
  "/documentation-lifecycle",
  "/linux-packages",
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
];
const publicRoutePrefixes = new Set([...selectorDestinations, ...rootRoutes]);
for (const [slug, routes] of Object.entries(projectRoutes)) {
  for (const route of routes) publicRoutePrefixes.add(publicRoute(slug, route));
}
publicRoutePrefixes.add("/pnport/install.sh");
publicRoutePrefixes.add("/pnport/install.ps1");
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
  return path.join(outputDirectory, slug, route.endsWith("/") ? route.slice(1) + "index.html" : `${route.slice(1)}.html`);
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

function visibleProjectText(contents) {
  return contents
    .replace(/<(?:script|style)\b[^>]*>[\s\S]*?<\/(?:script|style)>/giu, " ")
    .replace(/<[^>]*>/gu, " ")
    .replace(/\s+/gu, " ");
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
    if (slug === "pnport" && !/\bpnport 0\.1\.0\b[^.]{0,80}\b(?:unreleased|not (?:been )?(?:released|published))\b/iu.test(visibleProjectText(contents))) {
      failures.push(`${publicRoute(slug, route)} is missing its 0.1.0 unreleased notice`);
    }
    for (const destination of selectorDestinations) {
      if (!contents.includes(`href="${destination}"`) && !contents.includes(`href='${destination}'`)) {
        failures.push(`${publicRoute(slug, route)} is missing selector destination ${destination}`);
      }
    }
    if (slug === "clibox" || slug === "pnport" || slug === "react-forge") {
      const sidebar = contents.match(/<aside\b[^>]*class="[^"]*rp-doc-layout__sidebar[^"]*"[^>]*>[\s\S]*?<\/aside>/iu)?.[0] ?? "";
      const menuItems = [...contents.matchAll(/<a\b[^>]*role="menuitem"[^>]*>/giu)].map(([tag]) => tag);
      for (const destination of selectorDestinations) {
        if (!menuItems.some((tag) => tag.includes(`href="${destination}"`))) {
          failures.push(`${publicRoute(slug, route)} is missing a site-selector link`);
        }
      }
      const activeItems = menuItems.filter((tag) => tag.includes('aria-current="page"'));
      if (activeItems.length !== 1 || !activeItems[0].includes(`href="/${slug}/"`)) {
        failures.push(`${publicRoute(slug, route)} has an incorrect selected site`);
      }
      for (const child of routes) {
        if (!sidebar.includes(`href="${publicRoute(slug, child)}"`)) {
          failures.push(`${publicRoute(slug, route)} is missing a sidebar link`);
        }
      }
      const socialLinks = [...contents.matchAll(/<a\b[^>]*class="[^"]*rp-social-links__item[^"]*"[^>]*>/giu)].map(([tag]) => tag);
      if (!socialLinks.some((tag) => tag.includes('href="https://github.com/delinoio/oss"'))) {
        failures.push(`${publicRoute(slug, route)} is missing the repository social link`);
      }
      const footer = contents.match(/<footer\b[^>]*class="delino-repository-footer"[^>]*>[\s\S]*?<\/footer>/iu)?.[0] ?? "";
      if (!footer.includes('href="https://github.com/delinoio/oss"')) {
        failures.push(`${publicRoute(slug, route)} is missing the repository footer link`);
      }
    }
    const selected = (contents.match(/aria-current="page"/gu) ?? []).length;
    if (selected !== 1) failures.push(`${publicRoute(slug, route)} has ${selected} selected site destinations`);
    if (!contents.includes("https://github.com/delinoio/oss")) {
      failures.push(`${publicRoute(slug, route)} is missing the repository link`);
    }
    if (!contents.includes("delino-repository-footer")) {
      failures.push(`${publicRoute(slug, route)} is missing the document footer repository link`);
    }
    if (projectSecuritySlugs.has(slug)) {
      const publicText = visibleProjectText(contents);
      if (forbiddenProjectContent.some((pattern) => pattern.test(contents) || pattern.test(publicText))) {
        failures.push(`${publicRoute(slug, route)} contains prohibited public content`);
      }
      for (const link of collectSameOriginLinks(contents)) {
        try {
          const url = new URL(link, "https://oss.delino.io");
          if (url.username || url.password || url.searchParams.has("token") || url.searchParams.has("secret")) {
            failures.push(`${publicRoute(slug, route)} contains credential-bearing URL content`);
          }
        } catch {
          failures.push(`${publicRoute(slug, route)} contains a malformed public URL`);
        }
      }
    }
    for (const linkedRoute of routes) {
      const destination = publicRoute(slug, linkedRoute);
      if (!contents.includes(`href="${destination}"`) && !contents.includes(`href='${destination}'`)) {
        failures.push(`${publicRoute(slug, route)} is missing project navigation link ${destination}`);
      }
    }
    for (const oldOrigin of retiredOrigins) {
      if (contents.includes(oldOrigin)) failures.push(`${publicRoute(slug, route)} contains retired origin ${oldOrigin}`);
    }
    for (const link of collectSameOriginLinks(contents)) {
      if (/\.html(?:[?#]|$)/iu.test(link)) failures.push(`${publicRoute(slug, route)} contains an .html link: ${link}`);
      const linkPath = link.split(/[?#]/u, 1)[0];
      if (publicRoutePrefixes.has(linkPath)) continue;
      if (slug !== "pnport" && (linkPath.startsWith(`/${slug}/`) || linkPath === `/${slug}`)) continue;
      if (/^\/(?:assets|static)\//u.test(link)) continue;
      if (linkPath.startsWith("#")) continue;
      if (linkPath.startsWith("/")) failures.push(`${publicRoute(slug, route)} links to an unknown public route: ${link}`);
    }
  }
}

const installerChecks = [
  ["nodeup", "install.sh", "scripts/install/nodeup.sh"],
  ["nodeup", "install.ps1", "scripts/install/nodeup.ps1"],
  ["binpm", "install.sh", "scripts/install/binpm.sh"],
  ["binpm", "install.ps1", "scripts/install/binpm.ps1"],
  ["async-commit-hook", "install.sh", "scripts/install/async-commit-hook.sh"],
  ["async-commit-hook", "install.ps1", "scripts/install/async-commit-hook.ps1"],
  ["pnport", "install.sh", "scripts/install/pnport.sh"],
  ["pnport", "install.ps1", "scripts/install/pnport.ps1"],
];
for (const [slug, filename, source] of installerChecks) {
  const generated = path.join(outputDirectory, slug, filename);
  const sourceFile = new URL(`../../../${source}`, import.meta.url);
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
const expectedRedirects = [
  "/async-commit-hook/docs /async-commit-hook/docs/ 301",
  ...["pptx", "docx", "xlsx", "pdf", "figma", "sfx", "sprite"].flatMap((format) => [
    `/react-forge/${format} /react-forge/formats/${format}/ 301`,
    `/react-forge/${format}/ /react-forge/formats/${format}/ 301`,
  ]),
].join("\n") + "\n";
if (redirects !== expectedRedirects) failures.push("public documentation redirects are missing or incorrect");
const headers = await readFile(path.join(outputDirectory, "_headers"), "utf8").catch(() => "");
const expectedAsyncHeaders = `/async-commit-hook/*
  Referrer-Policy: no-referrer
  X-Content-Type-Options: nosniff
  X-Frame-Options: DENY
  Permissions-Policy: camera=(), microphone=(), geolocation=()
`;
if (headers !== expectedAsyncHeaders) failures.push("aggregate async security headers are missing or incorrect");

if (failures.length > 0) {
  console.error("Public docs project validation failed:");
  for (const failure of failures) console.error(`- ${failure}`);
  process.exit(1);
}

console.log("Public docs project validation passed.");
