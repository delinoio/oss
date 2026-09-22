import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { cp, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { after, before, test } from "node:test";
import { projectRoutes } from "./project-routes.mjs";

let fixtureDirectory;
const cleanValidator = fileURLToPath(new URL("./validate-clean-urls.mjs", import.meta.url));
const projectValidator = fileURLToPath(new URL("./validate-public-docs.mjs", import.meta.url));

before(async () => {
  fixtureDirectory = await mkdtemp(path.join(tmpdir(), "clibox-docs-validation-"));
  await cp(new URL("../doc_build", import.meta.url), path.join(fixtureDirectory, "doc_build"), { recursive: true });
});
after(async () => {
  if (fixtureDirectory) await rm(fixtureDirectory, { recursive: true, force: true });
});

function validate(script) {
  return spawnSync(process.execPath, [script], { cwd: fixtureDirectory, encoding: "utf8" });
}

async function rejectsMutation(script, mutate, classification, hiddenValue) {
  const file = path.join(fixtureDirectory, "doc_build/clibox/index.html");
  const original = await readFile(file, "utf8");
  const mutated = mutate(original);
  assert.notEqual(mutated, original, "fixture must change the rendered page");
  try {
    await writeFile(file, mutated);
    const result = validate(script);
    assert.equal(result.status, 1, result.stdout + result.stderr);
    assert.match(result.stderr, classification);
    if (hiddenValue) assert.ok(!result.stderr.includes(hiddenValue), "diagnostic exposed rejected content");
  } finally {
    await writeFile(file, original);
  }
}

test("both validators accept the complete generated site", () => {
  for (const script of [cleanValidator, projectValidator]) {
    const result = validate(script);
    assert.equal(result.status, 0, result.stdout + result.stderr);
  }
});

test("article links cannot replace a missing sidebar destination", async () => {
  await rejectsMutation(projectValidator, (html) => html.replace(
    /(<aside\b[^>]*class="[^"]*rp-doc-layout__sidebar[^"]*"[^>]*>)([\s\S]*?)(<\/aside>)/u,
    (_, start, sidebar, end) => start + sidebar.replace('href="/clibox/install"', 'href="/clibox/"') + end,
  ), /clibox\/.*missing a sidebar link/u);
});

test("a selected peer cannot stand in for the selected clibox destination", async () => {
  await rejectsMutation(projectValidator, (html) => html.replace(
    /<a\b[^>]*aria-current="page"[^>]*>/u,
    (tag) => tag.replace('href="/clibox/"', 'href="/runmoor/"'),
  ), /incorrect selected site/u);
});

for (const region of ["rp-social-links__item", "delino-repository-footer__link"]) {
  test(`repository links outside ${region} cannot satisfy its check`, async () => {
    await rejectsMutation(projectValidator, (html) => html.replace(
      new RegExp(`<a\\b[^>]*class="${region}"[^>]*>`, "gu"),
      (tag) => tag.replace('href="https://github.com/delinoio/oss"', 'href="/"'),
    ), /missing the repository (?:social|footer) link/u);
  });
}

test("the article must retain its required headings", async () => {
  await rejectsMutation(cleanValidator, (html) => html.replace('What you can do', 'Removed section'), /missing required heading/u);
});

for (const [name, fragment] of [
  ["encoded clean route", '<a href="/clibox/install&#46;html">Install</a>'],
  ["encoded credential", '<img src="https://example.com/image?%74oken=fixture-sensitive">'],
  ["hash-routed credential", '<a href="https://example.com/#/callback?code=fixture-sensitive">Link</a>'],
  ["private comment", '<!-- crates/clibox/private-fixture -->'],
  ["encoded private resource", '<img src="%2e%2e/%2e%2e/crates/clibox/private-fixture.png">'],
  ["public route prefix is not a private-path exception", '<!-- /clibox/install/private-fixture -->'],
]) {
  test(`rejects ${name} without disclosing supplied values`, async () => {
    await rejectsMutation(cleanValidator, (html) => html.replace('</main>', fragment + '</main>'),
      /(?:prohibited public content|links to \/clibox\/install.html)/u, 'fixture-sensitive');
  });
}

test("shared CSS applies credential and clean-route checks", async () => {
  const file = path.join(fixtureDirectory, "doc_build/clibox-fixture.css");
  try {
    for (const contents of [
      '.x { background: url("https://example.com/image?token=fixture-sensitive") }',
      '@import "/clibox/install.html";',
      '.x { background: url("file:///private/fixture-sensitive.png") }',
    ]) {
      await writeFile(file, contents);
      const result = validate(cleanValidator);
      assert.equal(result.status, 1);
      assert.match(result.stderr, /clibox-fixture.css contains (?:prohibited public content|a non-clean route)/u);
      assert.ok(!result.stderr.includes('fixture-sensitive'));
    }
  } finally {
    await rm(file, { force: true });
  }
});


test("stylesheet path exceptions accept only complete project routes", async () => {
  const file = path.join(fixtureDirectory, "doc_build/route-fixture.css");
  try {
    const routes = Object.entries(projectRoutes).flatMap(([slug, children]) => children.map((route) => `/${slug}${route}`));
    await writeFile(file, routes.map((route) => `@import "${route}#section";`).join("\n"));
    const accepted = validate(cleanValidator);
    assert.equal(accepted.status, 0, accepted.stdout + accepted.stderr);
    for (const slug of Object.keys(projectRoutes)) {
      for (const target of [`/${slug}/private-fixture.css`, `/${slug}/internal/secret.png`, `/${slug}/commands/private.png`, `/${slug}/%69nternal/secret.png`]) {
        await writeFile(file, `.x { background: url("${target}") }`);
        const rejected = validate(cleanValidator);
        assert.equal(rejected.status, 1, `${target}: ${rejected.stdout}${rejected.stderr}`);
        assert.match(rejected.stderr, /route-fixture.css contains prohibited public content/u);
        assert.ok(!rejected.stderr.includes(target), "diagnostic exposed a rejected path");
      }
    }
  } finally {
    await rm(file, { force: true });
  }
});


test("shared stylesheets reject raw credentials in comments and custom properties", async () => {
  const file = path.join(fixtureDirectory, "doc_build/raw-credential-fixture.css");
  try {
    for (const [contents, hiddenValue] of [
      ['/* Authorization: Bearer fixture-sensitive */', 'fixture-sensitive'],
      [':root { --api-key: fixture-sensitive; }', 'fixture-sensitive'],
      ['/* {"refresh_token":"fixture-sensitive"} */', 'fixture-sensitive'],
      ['/* password: fixture-sensitive */', 'fixture-sensitive'],
      ['/* GH_TOKEN=fixture-sensitive */', 'fixture-sensitive'],
      ['/* DEVHUD_UPLOAD_SECRET=fixture-sensitive */', 'fixture-sensitive'],
      ['/* ghp_fixture_sensitive */', 'ghp_fixture_sensitive'],
      ['/* github_pat_fixture_sensitive */', 'github_pat_fixture_sensitive'],
    ]) {
      await writeFile(file, contents);
      const result = validate(cleanValidator);
      assert.equal(result.status, 1, result.stdout + result.stderr);
      assert.match(result.stderr, /raw-credential-fixture.css contains prohibited public content/u);
      assert.ok(!result.stderr.includes(hiddenValue), "diagnostic exposed a rejected credential");
    }
  } finally {
    await rm(file, { force: true });
  }
});
