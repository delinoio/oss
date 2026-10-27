// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import {
  mkdir,
  mkdtemp,
  readFile,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  fingerprint,
  generate,
  inspect,
  loadProject,
  localizeLinks,
  writeJson,
} from "./catalog.mjs";

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), "source-only-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const registry = {
    sourceLang: "en",
    locales: [
      { lang: "en", label: "English" },
      { lang: "ko", label: "Korean" },
    ],
    sourceOnly: {
      pages: ["services/perfine/index.md"],
      messages: ["perfine"],
      sections: { "index.md": ["perfine"] },
    },
  };
  await writeJson(join(root, "i18n/locales.json"), registry);
  await writeJson(join(root, "i18n/messages/en.json"), {
    shared: "Shared",
    perfine: "Perfine guide",
  });
  await writeJson(join(root, "i18n/messages/ko.json"), {
    shared: "Translated",
  });
  await mkdir(join(root, "docs/en/services/perfine"), { recursive: true });
  await mkdir(join(root, "docs/ko"), { recursive: true });
  const source = "# Home\n\nShared text.\n";
  const english = `${source}<!-- i18n:source-only perfine -->\n[Perfine](/services/perfine/)\n<!-- i18n:end -->\n`;
  await writeFile(join(root, "docs/en/index.md"), english);
  await writeFile(
    join(root, "docs/en/services/perfine/index.md"),
    "# Perfine\n\n[Home](/)\n",
  );
  await writeFile(join(root, "docs/ko/index.md"), source);
  await writeJson(join(root, "i18n/state.json"), {
    ko: {
      "messages/shared": fingerprint("Shared"),
      "docs/index.md": fingerprint(source),
    },
  });
  return { root, registry, english };
}

test("English-only discovery preserves existing hashes and excludes only declared units from generation", async (t) => {
  const { root } = await fixture(t);
  let project = await loadProject(root);
  assert.equal(project.units.length, 4);
  assert.deepEqual((await inspect(project)).issues, []);
  const state = await readFile(join(root, "i18n/state.json"), "utf8");
  assert.equal(
    await generate(project, () => assert.fail("no translation needed")),
    0,
  );
  assert.equal(await readFile(join(root, "i18n/state.json"), "utf8"), state);
  await writeFile(join(root, "docs/en/services/perfine/new.md"), "# New\n");
  project = await loadProject(root);
  assert.deepEqual(
    (await inspect(project)).pending.map(({ unit }) => unit.id),
    ["docs/services/perfine/new.md"],
  );
  await generate(project, async (_locale, input) => input);
  assert.deepEqual((await inspect(await loadProject(root))).issues, []);
});

test("source-only sections do not hide shared source changes or invalid translations", async (t) => {
  const { root, english } = await fixture(t);
  await writeFile(
    join(root, "docs/en/index.md"),
    english.replace("[Perfine]", "[English guide]"),
  );
  assert.deepEqual((await inspect(await loadProject(root))).issues, []);
  await writeFile(
    join(root, "docs/en/index.md"),
    english.replace("Shared text.", "Updated shared text."),
  );
  assert.match(
    (await inspect(await loadProject(root))).issues.join(),
    /source changed/u,
  );
  await writeFile(
    join(root, "docs/ko/index.md"),
    "# Home\n\n[Broken](/ko/services/perfine/)\n",
  );
  assert.match(
    (await inspect(await loadProject(root))).issues.join(),
    /Unknown document route/u,
  );
});

test("obsolete source-only translations and state are rejected without deleting artifacts", async (t) => {
  const { root } = await fixture(t);
  await mkdir(join(root, "docs/ko/services/perfine"), { recursive: true });
  await writeFile(
    join(root, "docs/ko/services/perfine/index.md"),
    "# Unexpected\n",
  );
  await writeJson(join(root, "i18n/messages/ko.json"), {
    shared: "Translated",
    perfine: "Unexpected",
  });
  const state = JSON.parse(
    await readFile(join(root, "i18n/state.json"), "utf8"),
  );
  state.ko["docs/services/perfine/index.md"] = fingerprint("# Unexpected");
  await writeJson(join(root, "i18n/state.json"), state);
  const data = await loadProject(root);
  assert.equal((await inspect(data)).issues.length, 2);
  await assert.rejects(
    generate(data, () => assert.fail("no inference")),
    /obsolete translation/u,
  );
  assert.equal(
    await readFile(join(root, "docs/ko/services/perfine/index.md"), "utf8"),
    "# Unexpected\n",
  );
});

test("declarations reject stale paths, keys, duplicates, glob patterns, and malformed sections", async (t) => {
  const { root, registry, english } = await fixture(t);
  for (const policy of [
    { pages: ["missing.md"] },
    { pages: ["services/perfine/*.md"] },
    { messages: ["missing"] },
    { messages: ["perfine", "perfine"] },
    { pages: ["../secret.md"] },
    { typo: [] },
    { sections: { "missing.md": ["perfine"] } },
    { sections: { "index.md": ["perfine", "perfine"] } },
  ]) {
    await writeJson(join(root, "i18n/locales.json"), {
      ...registry,
      sourceOnly: policy,
    });
    await assert.rejects(loadProject(root), /source-only/iu);
  }
  await writeJson(join(root, "i18n/locales.json"), registry);
  for (const text of [
    english.replace("<!-- i18n:end -->", ""),
    english.replace("perfine -->", "unknown -->"),
    english + "<!-- i18n:source-only perfine -->\nAgain\n<!-- i18n:end -->\n",
  ]) {
    await writeFile(join(root, "docs/en/index.md"), text);
    await assert.rejects(loadProject(root), /source-only/iu);
  }
});

test("MDX, symlinks, empty pages, duplicate routes and broken English links cannot bypass discovery", async (t) => {
  const { root } = await fixture(t);
  const path = join(root, "docs/en/services/perfine/extra.md");
  await writeFile(path, "");
  await assert.rejects(loadProject(root), /Empty source page/u);
  await rm(path);
  await symlink("index.md", path);
  await assert.rejects(loadProject(root), /symbolic links/u);
  await rm(path);
  await writeFile(`${path}x`, "# MDX\n");
  await assert.rejects(loadProject(root), /MDX/u);
  await rm(`${path}x`);
  await writeFile(join(root, "docs/en/services/perfine.md"), "# Duplicate\n");
  await assert.rejects(loadProject(root), /Duplicate document route/u);
  await rm(join(root, "docs/en/services/perfine.md"));
  await writeFile(path, "# Broken\n\n[Missing](/services/perfine/missing)\n");
  await assert.rejects(loadProject(root), /Unknown document route/u);
});

test("link localization keeps English-only destinations, suffixes and literal code", () => {
  const input =
    '[Perfine](/services/perfine/index.html?q=1#intro) [Shared](/guide/)\n[x]: /services/perfine/\nlink: "/services/perfine/"\n`[Code](/guide/)`';
  assert.equal(
    localizeLinks(input, "ko", "en", ["/services/perfine"]),
    input.replace("](/guide/)", "](/ko/guide/)"),
  );
});

test("formatted source-only sections preserve hashes and literal marker examples", async (t) => {
  const { root, english } = await fixture(t);
  await writeFile(
    join(root, "docs/en/index.md"),
    english
      .replace("-->\n[Perfine]", "-->\n\n[Perfine]")
      .replace("\n<!-- i18n:end -->\n", "\n\n<!-- i18n:end -->\n\n"),
  );
  assert.deepEqual((await inspect(await loadProject(root))).issues, []);
  await writeFile(
    join(root, "docs/en/services/perfine/index.md"),
    "# Perfine\n\n```md\n<!-- i18n:source-only example -->\nExample\n<!-- i18n:end -->\n```\n",
  );
  assert.deepEqual((await inspect(await loadProject(root))).issues, []);
});

test("generation retains source-only links but still tracks shared keys and pages", async (t) => {
  const { root } = await fixture(t);
  await writeFile(
    join(root, "docs/en/shared.md"),
    "# Shared\n\n[English guide](/services/perfine/?q=1#intro) [Home](/)\n",
  );
  await writeJson(join(root, "i18n/messages/en.json"), {
    shared: "Changed",
    perfine: "Changed English guide",
    newKey: "New shared text",
  });
  const calls = [];
  await generate(await loadProject(root), async (_locale, input) => {
    calls.push(...Object.keys(input));
    return input;
  });
  assert.deepEqual(calls.sort(), [
    "docs/shared.md",
    "messages/newKey",
    "messages/shared",
  ]);
  assert.match(
    await readFile(join(root, "docs/ko/shared.md"), "utf8"),
    /\[English guide\]\(\/services\/perfine\/\?q=1#intro\) \[Home\]\(\/ko\/\)/u,
  );
  assert.deepEqual((await inspect(await loadProject(root))).issues, []);
});
