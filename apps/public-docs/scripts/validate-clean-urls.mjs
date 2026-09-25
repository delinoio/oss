import { access, readdir, readFile, stat } from "node:fs/promises";
import path from "node:path";
import { decodeHTML } from "entities";
import { projectRoutes } from "./project-routes.mjs";

const cliboxHeadings = {
  "/clibox/releases": [
    "Releases and verification",
    "Choose a version",
    "Distribution and verification",
    "Native package availability",
    "Use an earlier version"
  ],
  "/clibox/troubleshooting": [
    "Troubleshooting clibox",
    "Missing native package or version mismatch",
    "Arguments, timestamps, and files",
    "Desktop commands",
    "CPU counts",
    "Waits and configuration",
    "Diagnostics and support",
    "Validation limits and recovery"
  ],
  "/clibox/commands": [
    "Command index",
    "Help and version"
  ],
  "/clibox/output": [
    "Output and cancellation",
    "Input, output, and exit codes",
    "File replacement",
    "Utility diagnostics and operation limits"
  ],
  "/clibox/install": [
    "Install clibox",
    "Pin with pnpm",
    "Pin with npm",
    "Requirements",
    "Linux release archives",
    "Linux APT and DNF",
    "Desktop prerequisites"
  ],
  "/clibox/migration": [
    "Migrating older command syntax"
  ],
  "/clibox/getting-started": [
    "Getting started with clibox",
    "Install and check the version",
    "Try commands without changing files",
    "Use a package script",
    "Process local configuration"
  ],
  "/clibox/transformations": [
    "Text, time, Base64, and hashes",
    "Text replacement",
    "Time formatting and arithmetic",
    "Base64",
    "Hashes and verification"
  ],
  "/clibox/": [
    "clibox",
    "What you can do",
    "Supported environments",
    "Predictable scripts",
    "Learn more"
  ],
  "/clibox/configuration": [
    "Configuration commands",
    "List dotenv keys",
    "Merge dotenv files",
    "Normalize YAML",
    "Input limits and output safety",
    "Exit codes and diagnostics"
  ],
  "/clibox/system": [
    "System commands",
    "Query CPU counts",
    "Run with environment variables",
    "Inspect and terminate port owners",
    "Open a resource",
    "Copy and paste text"
  ],
  "/clibox/wait": [
    "Readiness waits",
    "TCP",
    "HTTP",
    "Files",
    "Results and cancellation"
  ]
};

const pnportHeadings = {
  "/pnport/": ["pnport", "Release targets", "What the CLI is designed to do", "Guides"],
  "/pnport/installation": ["Installation and availability", "Planned distribution", "Before a future install"],
  "/pnport/getting-started": ["Getting started", "Check the project", "Run a command"],
  "/pnport/commands": ["Commands", "Global options", "Machine-readable doctor output"],
  "/pnport/filesystem-and-processes": ["Filesystem and processes", "Dependency view", "Child processes and watches", "Current limits"],
  "/pnport/editors": ["Editors and language servers", "Configure the executable", "Recovery"],
  "/pnport/cache": ["Cache management", "Inspect the cache", "Prune or clean"],
  "/pnport/diagnostics": ["Diagnostics and troubleshooting", "Start with doctor", "Exit codes and streams"],
  "/pnport/benchmarks": ["Benchmarks", "Reproduction protocol"],
  "/pnport/releases": ["Releases and rollback", "Release readiness", "Explicit updates and rollback"],
};

const reactForgeHeadings = {
  "/react-forge/": ["React Forge", "Start with a presentation", "Where it runs", "Choose a workflow"],
  "/react-forge/installation": ["Install React Forge", "Requirements", "Verify the installation"],
  "/react-forge/getting-started": ["Getting started", "Next steps"],
  "/react-forge/sessions": ["Sessions and common API", "Fonts and assets", "Inspection and measurement", "Lifecycle and diagnostics"],
  "/react-forge/pptx": ["Author PPTX presentations"],
  "/react-forge/docx": ["Author DOCX documents"],
  "/react-forge/xlsx": ["Author XLSX workbooks"],
  "/react-forge/pdf": ["Author tagged PDF"],
  "/react-forge/sfx": ["Game sound effects (WAV)"],
  "/react-forge/sprite": ["Pixel sprites and animation", "Create an animated sprite", "Limits and boundaries"],
  "/react-forge/office-editing": ["Edit existing Office files"],
  "/react-forge/figma": ["Figma Design creation and editing", "Reopen and edit", "Publication outcomes and receipts"],
  "/react-forge/cli": ["One-shot CLI tasks"],
  "/react-forge/mcp": ["Local MCP sessions", "Typical local document sequence", "Figma and failure recovery"],
  "/react-forge/limits-and-troubleshooting": ["Limits and troubleshooting", "Common failures", "Boundaries"],
  "/react-forge/releases": ["Releases and validation", "Choose by availability", "Validation scope", "Update or roll back"],
};

const stableRouteIds = [
  ...Object.entries(projectRoutes).flatMap(([slug, routes]) => routes.map((route) => `/${slug}${route}`)),
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
const projectSlugs = Object.keys(projectRoutes).filter((slug) => slug !== "clibox" && slug !== "pnport" && slug !== "react-forge");

const outputDir = path.resolve("doc_build");
const stableRoutePathPattern = stableRouteIds
  .filter((routeId) => routeId !== "/")
  .sort((left, right) => right.length - left.length)
  .map((routeId) => RegExp.escape(routeId.slice(1)).replace(/\/$/u, "/?"))
  .join("|");
const routeOutputFiles = stableRouteIds.map((routeId) => ({
  routeId,
  outputFile:
    routeId.endsWith("/")
      ? path.join(outputDir, routeId.slice(1), "index.html")
      : path.join(outputDir, `${routeId.slice(1)}.html`),
}));
const htmlRoutePaths = new Set(
  routeOutputFiles.map(({ outputFile }) => `/${path.relative(outputDir, outputFile).split(path.sep).join("/")}`),
);
const urlAttributePattern = /\b(?:href|src|srcset|poster|action|formaction|data)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'`=<>]+))/giu;
const cssUrlPattern = /\burl\s*\(\s*(?:"([^"]*)"|'([^']*)'|([^\s)]+))\s*\)/giu;
const cssImportPattern = /@import\s+(?:"([^"]*)"|'([^']*)')/giu;
const validatorOrigin = "https://public-docs.invalid";

async function pathExists(filePath) {
  try {
    await access(filePath);
    return true;
  } catch {
    return false;
  }
}

async function collectCssFiles(directory) {
  const files = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const file = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...await collectCssFiles(file));
    else if (entry.isFile() && entry.name.endsWith(".css")) files.push(file);
  }
  return files;
}

async function collectHtmlFiles(directory, depth = 0) {
  const entries = await readdir(directory);
  const htmlFiles = [];

  for (const entry of entries) {
    const entryPath = path.join(directory, entry);
    const entryStat = await stat(entryPath);

    if (entryStat.isDirectory()) {
      if (depth === 0 && projectSlugs.includes(entry)) continue;
      htmlFiles.push(...(await collectHtmlFiles(entryPath, depth + 1)));
    } else if (entryPath.endsWith(".html") && entry !== "404.html") {
      htmlFiles.push(entryPath);
    }
  }

  return htmlFiles;
}

const htmlFiles = await collectHtmlFiles(outputDir);
const failures = [];

// Existing project sections have separate validators. clibox, pnport, and React Forge also
// use the strict article/resource checks below, with exact route exceptions.
for (const slug of projectSlugs) {
  if (!(await pathExists(path.join(outputDir, slug)))) {
    failures.push(`${slug} is missing from the public documentation tree`);
  }
}

function attributeValue(match) {
  return match[1] ?? match[2] ?? match[3] ?? "";
}

const requiredHeadings = new Map([
  ...Object.entries(cliboxHeadings),
  ...Object.entries(pnportHeadings),
  ...Object.entries(reactForgeHeadings),
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
  ["/clibox/", ["/clibox/install", "/clibox/getting-started", "/clibox/commands", "/clibox/migration"]],
  ["/clibox/install", ["/clibox/releases", "https://oss.delino.io/linux-packages"]],
  ["/clibox/commands", ["/clibox/system#query-cpu-counts", "/clibox/system#run-with-environment-variables", "/clibox/transformations#hashes-and-verification", "/clibox/wait#http", "/clibox/configuration#normalize-yaml"]],
  ["/pnport/", ["/pnport/installation", "/pnport/getting-started", "/pnport/commands", "/pnport/filesystem-and-processes", "/pnport/editors", "/pnport/cache", "/pnport/diagnostics", "/pnport/benchmarks", "/pnport/releases"]],
  ["/pnport/installation", ["/pnport/getting-started", "/pnport/commands", "/pnport/releases"]],
  ["/pnport/diagnostics", ["/pnport/cache"]],
  ["/react-forge/", ["/react-forge/installation", "/react-forge/getting-started", "/react-forge/sessions", "/react-forge/pptx", "/react-forge/docx", "/react-forge/xlsx", "/react-forge/pdf", "/react-forge/office-editing", "/react-forge/figma", "/react-forge/cli", "/react-forge/mcp", "/react-forge/sfx", "/react-forge/sprite", "/react-forge/limits-and-troubleshooting", "/react-forge/releases"]],
  ["/react-forge/installation", ["/react-forge/getting-started", "/react-forge/releases", "/react-forge/figma"]],
  ["/react-forge/figma", ["/react-forge/cli", "/react-forge/mcp", "/react-forge/limits-and-troubleshooting"]],
  ["/react-forge/mcp", ["/react-forge/figma"]],
  ["/", ["https://oss.delino.io/runmoor/", "https://oss.delino.io/nodeup/", "https://oss.delino.io/binpm/", "https://oss.delino.io/async-commit-hook/", "https://oss.delino.io/clibox/", "https://oss.delino.io/pnport/", "https://oss.delino.io/react-forge/"]],
  ["/projects-overview", ["https://oss.delino.io/runmoor/", "https://oss.delino.io/nodeup/", "https://oss.delino.io/binpm/", "https://oss.delino.io/async-commit-hook/", "https://oss.delino.io/clibox/", "https://oss.delino.io/pnport/", "https://oss.delino.io/react-forge/"]],
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

function htmlComments(contents) {
  return [...contents.matchAll(/<!--([\s\S]*?)-->/gu)].map(([, comment]) => comment).join(" ");
}

function findHtmlRouteLinks(contents, htmlFile) {
  const pagePath = `/${path.relative(outputDir, htmlFile).split(path.sep).join("/")}`;
  const pageUrl = new URL(pagePath, validatorOrigin);
  const invalidRoutes = [];

  for (const match of contents.matchAll(urlAttributePattern)) {
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

const credentialQueryKey = /^(?:token|access[_-]?token|refresh[_-]?token|api[_-]?key|password|secret|code|oauth_code)$/iu;

function containsCredentialBearingQuery(url) {
  return containsCredentialBearingParameters(url.searchParams);
}

function containsCredentialBearingFragment(url) {
  if (!url.hash) return false;
  const fragment = url.hash.slice(1).replace(/^\?/u, "");
  return containsCredentialBearingParameters(new URLSearchParams(fragment))
    || (fragment.includes("?") && containsCredentialBearingParameters(new URLSearchParams(fragment.slice(fragment.indexOf("?") + 1))));
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
  for (const match of contents.matchAll(cssUrlPattern)) targets.push(attributeValue(match).trim());
  for (const match of contents.matchAll(cssImportPattern)) targets.push(attributeValue(match).trim());
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
// Only the package registration page may show these exact public installation paths.
// Keep all other filesystem and repository-path checks unchanged.
const packageInstallationPaths = ['/usr/share/keyrings/delino-packages.gpg', '/usr/share/keyrings', '/etc/apt/sources.list.d/delino.sources', '/etc/apt/sources.list.d/delino-preview.sources', '/etc/yum.repos.d/delino.repo', '/etc/yum.repos.d/delino-preview.repo'];
function publicPathText(text, htmlFile) {
  if (path.relative(outputDir, htmlFile) !== 'linux-packages.html') return text;
  for (const allowed of [...packageInstallationPaths].sort((a, b) => b.length - a.length)) {
    text = text.replaceAll(new RegExp(RegExp.escape(allowed) + '(?=$|[\\s"\'<>])', 'gu'), 'public-installation-path');
  }
  return text;
}
// The terminal boundary must apply to every alternative, not just static assets.
const allowedPublicPathPattern = `(?:${stableRoutePathPattern}|pnport/install\\.(?:sh|ps1)|(?:assets|static)(?:[/\\\\][A-Za-z0-9._~-]+)*)`;
const forbiddenPathContent = [
  new RegExp(`(?:^|[\\s("'\\x60>])/(?!${allowedPublicPathPattern}(?:\\.html)?(?:[?#"'\\x60<\\s]|$))[A-Za-z0-9._~-]+(?:[/\\\\][^\\s"'\\x60<>]*)?`, "u"),
  /(?:^|[\s("'`>])(?:\.\.[\\/])+(?:[A-Za-z0-9._~-]+[\\/])+[^\s"'`<>]*/u,
  /(?:^|[\s("'`>])(?:apps|cmds|crates|docs|packaging|packages|protos|scripts|servers)(?:[\\/][^\s"'`<>]+)+/u,
  /(?:^|[\s("'`>])[A-Za-z]:[\\/][^\s"'`<>]*/u,
  /(?:^|[\s("'`>])\\\\[^\s"'`<>\\/]+[\\/][^\s"'`<>]*/u,
  /(?:^|[\s("'`>])file:\/\/(?:[^\/\s"'`<>]+)?\/[^\s"'`<>]*/iu,
];
const forbiddenLinkFixtures = [
  '<a href="https://alice:secret@example.com/support">support</a>',
  '<a href="https://example.com/?token&equals;abc123">support</a>',
  '<a href="https://example.com/callback?code&equals;abc123">callback</a>',
  '<a href="https://example.com/callback?oauth_code&equals;abc123">callback</a>',
];
const invalidRouteAttributeFixtures = [
  '<a href = "/devhud/install.html">install</a>',
  "<a href=/devhud/install.html>install</a>",
  '<a href="/devhud/install&#46;html">install</a>',
  '<form action="/devhud/install.html"></form>',
  '<button formaction="/devhud/support.html">Support</button>',
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
const forbiddenCssResourceFixtures = [
  '<div style="background-image:url(../../servers/devhud/private.png)"></div>',
  '<style>.private { background: url("file:///etc/devhud/private.png") }</style>',
  '<style>@import "../../servers/devhud/private.css";</style>',
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
  const commentText = htmlComments(contents);
  const pageUrl = new URL(`/${path.relative(outputDir, htmlFile).split(path.sep).join("/")}`, validatorOrigin);
  for (const match of contents.matchAll(urlAttributePattern)) {
    let target;
    try {
      target = new URL(decodeHTML(attributeValue(match)), pageUrl);
    } catch {
      continue;
    }
  }

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
    if (pattern.test(commentText) || pattern.test(publicPathText(renderedText, htmlFile)) || hrefTargets.some((target) => pattern.test(target))) {
      failures.push(`${path.relative(outputDir, htmlFile)} contains prohibited public content`);
    }
  }
  if (containsAffirmativeReleaseClaim(renderedText)) {
    failures.push(`${path.relative(outputDir, htmlFile)} contains prohibited public content`);
  }
}

for (const cssFile of await collectCssFiles(outputDir)) {
  const contents = await readFile(cssFile, "utf8");
  const relativeFile = path.relative(outputDir, cssFile);
  if (
    forbiddenContent.some((pattern) => pattern.test(contents))
    || containsCredentialBearingResource(contents, cssFile)
    || containsForbiddenResourcePath(contents, cssFile)
  ) {
    failures.push(`${relativeFile} contains prohibited public content`);
  }
  for (const target of resourceTargets(contents)) {
    try {
      const url = new URL(decodeHTML(target), new URL(`/${relativeFile}`, validatorOrigin));
      if (url.origin === validatorOrigin && htmlRoutePaths.has(url.pathname)) {
        failures.push(`${relativeFile} contains a non-clean route`);
      }
    } catch {
      failures.push(`${relativeFile} contains a malformed resource URL`);
    }
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

const forbiddenCommentFixture = "<!-- repository path: servers/devhud/config -->";
if (!forbiddenPathContent.some((pattern) => pattern.test(forbiddenCommentFixture))) {
  failures.push("forbidden HTML comment path fixture was not detected");
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

for (const fixture of forbiddenCssResourceFixtures) {
  if (!containsForbiddenResourcePath(fixture, path.join(outputDir, "fixture.html"))) {
    failures.push("forbidden CSS resource path fixture was not detected");
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

for (const routeId of ["/react-forge/sfx", "/react-forge/sprite"]) {
  const route = routeOutputFiles.find((entry) => entry.routeId === routeId);
  const contents = route ? await readFile(route.outputFile, "utf8") : "";
  const introduction = visibleText(articleContent(contents)).slice(0, 550);
  if (!/\bUnreleased\b/u.test(introduction) || !/\bnot included in npm 0\.1\.1\b/iu.test(introduction)) {
    failures.push(`${routeId} is missing its main-content npm 0.1.1 unreleased notice`);
  }
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
