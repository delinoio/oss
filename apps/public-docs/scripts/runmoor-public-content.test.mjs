import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { after, test } from "node:test";
import { fileURLToPath } from "node:url";

// Exercise the actual publication validator against copies of the real build.
// Synthetic secrets never enter the checkout's documents or generated output.
const buildDirectory = fileURLToPath(new URL("../doc_build/", import.meta.url));
const validator = fileURLToPath(new URL("./validate-clean-urls.mjs", import.meta.url));
const temporaryDirectory = mkdtempSync(path.join(tmpdir(), "runmoor-docs-content-"));
after(() => rmSync(temporaryDirectory, { recursive: true, force: true }));
const outputDirectory = path.join(temporaryDirectory, "doc_build");
mkdirSync(outputDirectory);
cpSync(buildDirectory, outputDirectory, { recursive: true });
const homeFile = path.join(outputDirectory, "index.html");
const indexFile = path.join(outputDirectory, "runmoor", "index.html");
const home = readFileSync(homeFile, "utf8");
const original = readFileSync(indexFile, "utf8");

function validateFixture(fixture) {
  return validatePage(original.replace("</body>", `${fixture}</body>`));
}

function validatePage(contents) {
  writeFileSync(indexFile, contents);
  return spawnSync(process.execPath, [validator], {
    cwd: temporaryDirectory,
    encoding: "utf8",
    timeout: 10_000,
  });
}

const stylesheetFile = path.join(outputDirectory, "static", "css", "fixture.css");
for (const [name, css] of [
  ["credential userinfo", '.private { background: url("https://reader:fixture-value@example.com/image.png") }'],
  ["credential query", '.private { background: url("https://example.com/image.png?code=fixture-value") }'],
  ["credential fragment", '.private { background: url("https://example.com/#/callback?oauth_code=fixture-value") }'],
  ["private file", '.private { background: url("file:///etc/private.png") }'],
  ["repository path", '.private { background: url("../../../servers/runmoor/private.png") }'],
  ["CSS import", '@import "../../../servers/runmoor/private.css";'],
]) {
  test(`emitted stylesheet rejects ${name} without exposing the value`, () => {
    writeFileSync(stylesheetFile, css);
    try {
      const result = validateFixture('<link rel="stylesheet" href="/static/css/fixture.css">');
      assert.equal(result.error, undefined);
      assert.equal(result.status, 1);
      assert.match(result.stderr, /static\/css\/fixture\.css contains prohibited public content/u);
      assert.doesNotMatch(result.stdout + result.stderr, /fixture-value|reader|private\.(?:png|css)/u);
    } finally {
      rmSync(stylesheetFile);
    }
  });
}

test("emitted stylesheets preserve generated fonts, public assets, and external resources", () => {
  writeFileSync(stylesheetFile, `
    @font-face { font-family: public; src: url(../media/font.woff2) }
    .public { background-image: url("/assets/logo.svg") }
    @import "https://example.com/theme.css";
  `);
  try {
    const result = validateFixture('<link rel="stylesheet" href="/static/css/fixture.css">');
    assert.equal(result.status, 0, result.stderr);
  } finally {
    rmSync(stylesheetFile);
  }
});

function removeLinks(contents, className, href) {
  return contents.replace(/<a\b[^>]*>[\s\S]*?<\/a>/giu, (anchor) => {
    const classes = anchor.match(/\bclass="([^"]*)"/u)?.[1].split(/\s+/u) ?? [];
    return classes.includes(className) && anchor.includes(`href="${href}"`) ? "" : anchor;
  });
}

test("the integrated site does not render the removed top navigation", () => {
  assert.doesNotMatch(original, /class="rp-nav-menu\b/u);
});

test("the product switcher exposes every site and marks the current route", () => {
  for (const href of [
    "https://oss.delino.io/",
    "https://oss.delino.io/runmoor",
    "https://nodeup.delino.io",
    "https://binpm.delino.io",
  ]) {
    assert.ok(original.includes(`href="${href}"`), `missing product link ${href}`);
  }
  assert.match(original, /aria-expanded="false"/u);
  assert.match(original, /aria-controls="docs-product-switcher-[^"]+"/u);
  assert.match(original, /aria-current="page"[^>]*class="[^"]*delino-docs-product-switcher__link[^"]*"[^>]*href="https:\/\/oss\.delino\.io\/runmoor"/u);
  assert.match(home, /aria-current="page"[^>]*class="[^"]*delino-docs-product-switcher__link[^"]*"[^>]*href="https:\/\/oss\.delino\.io\/"/u);
});

for (const route of [
  "/runmoor",
  "/runmoor/install",
  "/runmoor/configuration",
  "/runmoor/commands",
  "/runmoor/docker",
  "/runmoor/tart",
  "/runmoor/operations",
]) {
  test(`sidebar must link ${route} independently of article links`, () => {
    const modified = removeLinks(original, "rp-sidebar-item", route);
    assert.notEqual(modified, original);
    const result = validatePage(modified);
    assert.equal(result.status, 1);
    assert.ok(result.stderr.includes(`is missing sidebar link ${route}`), result.stderr);
  });
}

test("social repository navigation cannot be satisfied by the footer", () => {
  const modified = removeLinks(original, "rp-social-links__item", "https://github.com/delinoio/oss");
  assert.notEqual(modified, original);
  assert.ok(modified.includes('class="delino-repository-footer"'));
  const result = validatePage(modified);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /is missing the social navigation repository link/u);
});

for (const href of [
  "https://reader:fixture-value@[invalid",
  "https://reader:fixture-value@host:invalid",
  "https://[invalid",
]) {
  test(`malformed anchor URLs fail without exposing their input (${href.includes("fixture-value") ? "credential" : "plain"})`, () => {
    const result = validateFixture(`<a href="${href}">Invalid destination</a>`);
    assert.equal(result.error, undefined);
    assert.equal(result.status, 1);
    assert.match(result.stderr, /runmoor\/index\.html contains a malformed link/u);
    assert.doesNotMatch(result.stdout + result.stderr, /fixture-value|reader|ERR_INVALID_URL|input:/u);
  });
}

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

for (const [claim, prohibited] of releaseAvailabilityFixtures) {
  test(`release claims classify: ${claim}`, () => {
    const result = validateFixture(`<p>${claim}</p>`);
    assert.equal(result.error, undefined);
    assert.equal(result.status, prohibited ? 1 : 0, result.stderr);
    if (prohibited) assert.match(result.stderr, /runmoor\/index\.html contains an unsupported release claim/u);
  });
}

test("release claims inspect rendered text while preserving verification disclosures", () => {
  const rejected = validateFixture("<p><strong>beta</strong> channel is available.</p>");
  assert.equal(rejected.status, 1);
  assert.match(rejected.stderr, /runmoor\/index\.html contains an unsupported release claim/u);
  const accepted = validateFixture("<p>Runmoor is released through the stable channel. Live GitHub compatibility is not certified. No beta channel is available. Early-access channel is not supported.</p>");
  assert.equal(accepted.status, 0, accepted.stderr);
});

const forbidden = [
  ["classic PAT", "<p>ghp_fixture1234567890</p>"],
  ["fine-grained PAT", "<p>github_pat_fixture1234567890</p>"],
  ["rendered JSON credentials", '<code>{&quot;refresh_token&quot;:&quot;fixture-value&quot;,&quot;password&quot;:&quot;fixture-value&quot;}</code>'],
  ["bearer credential", "<p>Authorization: Bearer fixture-value</p>"],
  ["encoded PAT in a comment", "<!-- ghp&#95;fixture1234567890 -->"],
  ["credential URL userinfo", '<a href="https://alice:fixture-value@example.com/help">Help</a>'],
  ["credential URL query", '<a href="https://example.com/help?token&equals;fixture-value">Help</a>'],
  ["authorization code", '<a href="https://example.com/callback?code&equals;fixture-value">Callback</a>'],
  ["OAuth code", '<a href="https://example.com/callback?oauth_code&equals;fixture-value">Callback</a>'],
  ["credential URL fragment", '<a href="https://example.com/help#token&equals;fixture-value">Help</a>'],
  ["POSIX path", "<p>/etc/private.conf</p>"],
  ["Windows path", String.raw`<p>C:\Users\private\config</p>`],
  ["UNC path", String.raw`<p>\\server\share\private.conf</p>`],
  ["repository path", "<code>servers/runmoor/config</code>"],
  ["repository path comment", "<!-- repository path: servers/runmoor/config -->"],
  ["relative repository link", '<a href="../../servers/runmoor/config">Private</a>'],
  ["encoded relative path", '<img src="%2E%2E/%2E%2E/servers/runmoor/private.png">'],
  ["route-name prefix path", '<img src="/installer/private.png">'],
  ["file URL", '<a href="file://server/share/private.conf">Private</a>'],
  ["local file URL", '<a href="file://localhost/etc/private.conf">Private</a>'],
  ["encoded resource scheme", '<img src="file&colon;///etc/private.png">'],
  ["unquoted resource", '<img src=file:///etc/private.png>'],
  ["spaced resource", '<img src = "file:///etc/private.png">'],
  ["srcset second candidate", '<img srcset="/assets/logo.svg 1x, ../../servers/runmoor/private.png 2x">'],
  ["stylesheet credential URL", '<link rel="stylesheet" href="https://alice:fixture-value@example.com/style.css">'],
  ["non-anchor credential link", '<area href="https://example.com/map?token&equals;fixture-value">'],
  ["stylesheet file URL", '<link rel="stylesheet" href="file:///etc/private.css">'],
  ["inline CSS path", '<div style="background-image:url(../../servers/runmoor/private.png)"></div>'],
  ["CSS file URL", '<style>.private { background: url("file:///etc/private.png") }</style>'],
  ["CSS import", '<style>@import "../../servers/runmoor/private.css";</style>'],
  ["CSS credential URL", '<style>.private { background: url("https://example.com/image?token&equals;fixture-value") }</style>'],
  ["public asset with credentials", '<img src="/assets/logo.svg#token=fixture-value">'],
];

for (const [name, fixture] of [
  ["non-anchor href", '<area href="/runmoor/install.html">'],
  ["src", '<iframe src="/runmoor/install.html"></iframe>'],
  ["srcset second candidate", '<img srcset="/assets/logo.svg 1x, /runmoor/install.html 2x">'],
  ["poster", '<video poster="/runmoor/install.html"></video>'],
  ["action", '<form action="/runmoor/install.html"></form>'],
  ["formaction", '<button formaction="/runmoor/operations.html">Submit</button>'],
  ["data", '<object data="/runmoor/install.html"></object>'],
  ["relative attribute", '<form action="install.html?lang=en#verification"></form>'],
  ["encoded attribute", '<button formaction="/runmoor/install&period;html">Submit</button>'],
  ["absolute same-origin attribute", '<form action="https://public-docs.invalid/runmoor/install.html"></form>'],
  ["inline CSS", '<style>.link { background: url("/runmoor/install.html") }</style>'],
]) {
  test(`clean URLs reject HTML route in ${name}`, () => {
    const result = validateFixture(fixture);
    assert.equal(result.error, undefined);
    assert.equal(result.status, 1);
  assert.match(result.stderr, /runmoor\/index\.html links to \/runmoor\//u);
  });
}
for (const key of ["code", "oauth_code"]) {
  forbidden.push(
    [`direct ${key} fragment`, `<a href="https://example.com/#${key}&equals;fixture-value">Callback</a>`],
    [`routed ${key} fragment`, `<a href="https://example.com/#/callback?${key}=fixture-value">Callback</a>`],
    [`routed encoded ${key} fragment`, `<img src="https://example.com/#/callback?${key}&equals;fixture-value">`],
    [`routed CSS ${key} fragment`, `<style>.private { background: url("https://example.com/#/callback?${key}&equals;fixture-value") }</style>`],
  );
}
for (const route of ["install", "configuration", "commands", "docker", "tart", "operations"]) {
  forbidden.push(
    [`route-prefixed ${route} resource`, `<img src="/runmoor/${route}/servers/runmoor/private.png">`],
    [`route-prefixed ${route} text`, `<code>/runmoor/${route}/servers/runmoor/private.conf</code>`],
  );
}
for (const attribute of ["src", "poster", "action", "formaction", "data"]) {
  forbidden.push(
    [`${attribute} private path`, `<div ${attribute}="file:///etc/private.conf"></div>`],
    [`${attribute} credential userinfo`, `<div ${attribute}="https://alice:fixture-value@example.com/resource"></div>`],
    [`${attribute} credential query`, `<div ${attribute}="https://example.com/resource?token&equals;fixture-value"></div>`],
    [`${attribute} credential fragment`, `<div ${attribute}="https://example.com/resource#token&equals;fixture-value"></div>`],
  );
}

for (const [name, fixture] of forbidden) {
  test(`publication rejects ${name} without echoing the value`, () => {
    const result = validateFixture(fixture);
    assert.equal(result.error, undefined);
    assert.equal(result.status, 1);
    assert.match(result.stderr, /runmoor\/index\.html contains prohibited public content/u);
    assert.doesNotMatch(result.stdout + result.stderr, /fixture-value|ghp_fixture|github_pat_fixture|private\.(?:conf|png|css)/u);
  });
}

test("publication preserves public routes, assets, external references, and credential placeholders", () => {
  const result = validateFixture(`
    <a href="/runmoor/install#verification">Install</a>
    <a href="/runmoor/configuration?lang=en">Configuration</a>
    <a href="https://docs.example.com/guide.html">External reference</a>
    <form action="https://docs.example.com/install.html"></form>
    <button formaction="/runmoor/install?lang=en#verification">Install</button>
    <a href="https://docs.example.com/#/guide?section=installation">External routed reference</a>
    <img src="/assets/logo.svg">
    <img srcset="/assets/logo.svg 1x, /static/logo.svg 2x">
    <script src="/static/js/app.js"></script>
    <style>.public { background: url("/assets/logo.svg") }</style>
    <code>credential = { env = "RUNMOOR_PAT" }</code>
    <code>credential = { file = "REPLACE_WITH_ABSOLUTE_PRIVATE_FILE" }</code>
    <code>$XDG_CONFIG_HOME/runmoor/config.toml</code>
    <code>~/.config/runmoor/config.toml</code>
  `);
  assert.equal(result.error, undefined);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /validation passed/u);
});
