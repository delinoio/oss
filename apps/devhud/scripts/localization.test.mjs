import assert from "node:assert/strict";
import test from "node:test";

import { messages, selectSupportedLanguage } from "../src/localization.ts";
import { LanguagePreference, ThemePreference, readPreferences, resolveLanguage, resolveTheme } from "../src/shell.ts";

test("selects English from an English platform locale", () => {
  assert.equal(selectSupportedLanguage(["en-US"]), "en");
  assert.equal(messages.en.home, "Home");
});

test("selects Korean from Korean regional platform locales", () => {
  assert.equal(selectSupportedLanguage(["ko-KR"]), "ko");
  assert.equal(selectSupportedLanguage(["ko_KR"]), "ko");
  assert.equal(messages.ko.home, "홈");
});

test("resolves system preferences and safely falls back to defaults", () => {
  assert.equal(resolveLanguage(LanguagePreference.System, ["fr-FR"]), "en");
  assert.equal(resolveLanguage(LanguagePreference.System, ["ko-KR"]), "ko");
  assert.equal(resolveTheme(ThemePreference.System, true), ThemePreference.Dark);
  assert.equal(resolveTheme(ThemePreference.System, false), ThemePreference.Light);
  assert.equal(readPreferences({ getItem: () => "not json" }).language, LanguagePreference.System);
});

test("uses the first supported platform language", () => {
  assert.equal(selectSupportedLanguage(["fr-FR", "ko-KR", "en-US"]), "ko");
  assert.equal(selectSupportedLanguage(["fr-FR", "en-US", "ko-KR"]), "en");
});

test("falls back to English when no platform language is supported", () => {
  assert.equal(selectSupportedLanguage(["fr-FR"]), "en");
  assert.equal(selectSupportedLanguage([]), "en");
});
