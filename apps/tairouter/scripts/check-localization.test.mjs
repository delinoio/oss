// SPDX-License-Identifier: Apache-2.0
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { fingerprint } from "../../../scripts/localization/catalog.mjs";
import { checkLocalization } from "./check-localization.mjs";

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), "tairouter-localization-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  await mkdir(join(root, "i18n/messages"), { recursive: true });
  const write = (path, value) =>
    writeFile(join(root, path), JSON.stringify(value));
  await write("i18n/locales.json", {
    sourceLang: "en",
    locales: [
      { lang: "en", label: "English" },
      { lang: "ko", label: "한국어" },
    ],
  });
  await write("i18n/messages/en.json", { greeting: "Hello {value0}" });
  return { root, write };
}

test("unstarted languages are explicitly deferred without manufactured state", async (t) => {
  const { root } = await fixture(t);
  assert.deepEqual((await checkLocalization(root)).deferred, ["ko"]);
});

test("partial or untracked catalogs fail rather than being deferred", async (t) => {
  const { root, write } = await fixture(t);
  await write("i18n/messages/ko.json", {});
  await assert.rejects(checkLocalization(root), /nonempty string/);
  await write("i18n/messages/ko.json", { greeting: "Fixture {value0}" });
  await assert.rejects(checkLocalization(root), /translation is untracked/);
});

test("tracked translations pass, but stale sources and changed placeholders fail", async (t) => {
  const { root, write } = await fixture(t);
  await write("i18n/messages/ko.json", { greeting: "Fixture {value0}" });
  await write("i18n/state.json", {
    ko: { "messages/greeting": fingerprint("Hello {value0}") },
  });
  assert.deepEqual((await checkLocalization(root)).deferred, []);
  await write("i18n/messages/en.json", { greeting: "Welcome {value0}" });
  await assert.rejects(checkLocalization(root), /source changed/);
  await write("i18n/messages/ko.json", { greeting: "Fixture {value1}" });
  await assert.rejects(checkLocalization(root), /protected placeholders/);
});

test("removing a tracked catalog does not turn it into deferred work", async (t) => {
  const { root, write } = await fixture(t);
  await write("i18n/state.json", {
    ko: { "messages/greeting": fingerprint("Hello {value0}") },
  });
  await assert.rejects(checkLocalization(root), /nonempty string/);
});
