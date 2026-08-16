import assert from "node:assert/strict";
import test from "node:test";

import { selectSupportedLanguage, shellCopy } from "../src/localization.ts";

test("selects English from an English platform locale", () => {
  assert.equal(selectSupportedLanguage(["en-US"]), "en");
  assert.equal(shellCopy.en.eyebrow, "Desktop foundation");
});

test("selects Korean from Korean regional platform locales", () => {
  assert.equal(selectSupportedLanguage(["ko-KR"]), "ko");
  assert.equal(selectSupportedLanguage(["ko_KR"]), "ko");
  assert.equal(shellCopy.ko.eyebrow, "데스크톱 기반");
});

test("uses the first supported platform language", () => {
  assert.equal(selectSupportedLanguage(["fr-FR", "ko-KR", "en-US"]), "ko");
  assert.equal(selectSupportedLanguage(["fr-FR", "en-US", "ko-KR"]), "en");
});

test("falls back to English when no platform language is supported", () => {
  assert.equal(selectSupportedLanguage(["fr-FR"]), "en");
  assert.equal(selectSupportedLanguage([]), "en");
});
