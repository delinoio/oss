import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import { runInNewContext } from "node:vm";
import { decodeHTML } from "entities";
import test from "node:test";

const root = new URL("../", import.meta.url);
const routes = JSON.parse(await readFile(new URL("routes.json", root), "utf8"));
const read = (path) => readFile(new URL(path, root), "utf8");

test("all guides have clean routes, navigation, search and public content", async () => {
  for (const route of routes) {
    const html = await read(`doc_build/${route.link === "/" ? "index" : route.link.slice(1)}.html`);
    assert.match(html, /<main\b/);
    assert.match(html, /<h1\b/);
    assert.match(html, /Search/i);
    assert.match(html, /https:\/\/github.com\/delinoio\/oss/);
    for (const target of routes) assert.ok(html.includes(`href="${target.link}"`), `${route.link}: missing ${target.link}`);
    const text = decodeHTML(html.replace(/<script\b[^>]*>[\s\S]*?<\/script>/gi, "").replace(/<[^>]*>/g, " "));
    assert.doesNotMatch(text, /(?:\/Users\/|\/home\/[a-z]|\.infisical|DEVHUD_|ghp_[a-z0-9]{20}|github_pat_[a-z0-9_]{20}|BEGIN PRIVATE KEY)/i);
    assert.doesNotMatch(text, /(?:apps|cmds|servers|protos)\/(?:async-commit-hook|devhud)/);
    for (const [, href] of html.matchAll(/href="([^"]+)"/g)) {
      if (href.startsWith("/")) assert.doesNotMatch(href, /\.html(?:[?#]|$)/);
    }
  }
});

test("preserves guide subjects and complete user-facing examples", async () => {
  const required = {
    install: ["SHA256SUMS", "cosign", "--version", "Homebrew"],
    start: ["startup error", "Linked worktrees", "Open accepted execution"],
    configuration: ["go-test-json", "TEST_TOKEN", "106751", "credential references", "ACH_MANAGED"],
    validation: ["exact commit", "Optional", "pre-push"],
    commands: ["schema_version=1", "--cursor", "--timeout"],
    agents: ["Codex", "MCP", "agent-guide"],
    web: ["on-demand", "Ctrl-C", "no pairing", "Node", "local processes"],
    privacy: ["redact", "submodule", "Git LFS", "1 MiB"],
    recovery: ["Linux", "reboot", "--recover", "byte-for-byte"],
    compatibility: ["Windows", "Edge", "validation"],
    symlinks: ["Windows", "symlink"],
    "existing-hooks": ["Lefthook", "pre-push"],
  };
  for (const [route, fragments] of Object.entries(required)) {
    const content = (await read(`docs/${route}.md`)).toLowerCase();
    for (const fragment of fragments) assert.ok(content.includes(fragment.toLowerCase()), `${route}: missing ${fragment}`);
  }
});

test("published installation and upgrade commands follow release defaults", async () => {
  const code = async (route) => [...(await read(`doc_build/${route}.html`)).matchAll(/<code\b[^>]*>([\s\S]*?)<\/code>/g)]
    .map(([, value]) => decodeHTML(value.replace(/<[^>]*>/g, "")));
  const installation = await code("install");
  const recovery = await code("recovery");
  const lines = [...installation, ...recovery].flatMap((block) => block.split("\n"));
  for (const command of ["sh install-ach.sh", "./install-ach.ps1", "ach self-update"]) {
    assert.ok(lines.includes(command), `Missing unpinned primary command: ${command}`);
  }
  for (const command of [
    "sh install-ach.sh --version MAJOR.MINOR.PATCH",
    "./install-ach.ps1 -Version MAJOR.MINOR.PATCH",
  ]) assert.ok(installation.includes(command), `Missing explicit version guidance: ${command}`);
  assert.ok(recovery.includes("ach self-update --version MAJOR.MINOR.PATCH"));
  for (const line of lines) assert.doesNotMatch(line, /^(?:sh install-ach\.sh --version|\.\/install-ach\.ps1 -Version|ach self-update --version) \d/m);
});

test("legacy guide links map only to known documentation routes", async () => {
  const script = await read("doc_build/docs/redirect.js");
  for (const hash of ["", "#install", "#existing-hooks", "#pair=old&port=46309", "#https://evil.example"]) {
    let destination;
    runInNewContext(script, { window: { location: { hash, replace: (value) => { destination = value; } } } });
    assert.equal(destination, routes.some((route) => route.link === "/" + hash.slice(1)) ? "/" + hash.slice(1) : "/");
  }
  assert.equal(await read("doc_build/_redirects"), "/docs /docs/ 301\n");
});

test("installer endpoints retain canonical bytes and the site has no local API client", async () => {
  for (const name of ["install.sh", "install.ps1"]) {
    assert.equal(await read(`doc_build/${name}`), await read(`public/${name}`));
    assert.match(await read(`doc_build/${name}`), /verify-blob/);
  }
  for (const file of await readdir(new URL("doc_build/", root), { recursive: true })) {
    if (!file.endsWith(".js")) continue;
    assert.doesNotMatch(await read(`doc_build/${file}`), /async_commit_hook\.v1\.LocalService|ach-v1-browser-|X-Ach-Api-Version/);
  }
});
