import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
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
for (const entry of readdirSync(buildDirectory)) {
  if (entry.endsWith(".html")) copyFileSync(path.join(buildDirectory, entry), path.join(outputDirectory, entry));
}
const indexFile = path.join(outputDirectory, "index.html");
const original = readFileSync(indexFile, "utf8");

function validateFixture(fixture) {
  writeFileSync(indexFile, original.replace("</body>", `${fixture}</body>`));
  return spawnSync(process.execPath, [validator], {
    cwd: temporaryDirectory,
    encoding: "utf8",
    timeout: 10_000,
  });
}

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
    [`route-prefixed ${route} resource`, `<img src="/${route}/servers/runmoor/private.png">`],
    [`route-prefixed ${route} text`, `<code>/${route}/servers/runmoor/private.conf</code>`],
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
    assert.match(result.stderr, /index\.html contains prohibited public content/u);
    assert.doesNotMatch(result.stdout + result.stderr, /fixture-value|ghp_fixture|github_pat_fixture|private\.(?:conf|png|css)/u);
  });
}

test("publication preserves public routes, assets, external references, and credential placeholders", () => {
  const result = validateFixture(`
    <a href="/install#verification">Install</a>
    <a href="/configuration?lang=en">Configuration</a>
    <a href="https://docs.example.com/guide.html">External reference</a>
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
