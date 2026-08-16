import assert from "node:assert/strict";
import test from "node:test";

import { messages, selectSupportedLanguage } from "../src/localization.ts";
import { LanguagePreference, ThemePreference, completeOnboarding, defaultPreferences, hasCompletedOnboarding, isValidApiOrigin, readPreferences, resolveLanguage, resolveTheme } from "../src/shell.ts";

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
  assert.equal(resolveLanguage(LanguagePreference.System, ["en-US", "ko-KR"]), "en");
});

test("sanitizes each persisted preference independently", () => {
  const stored = JSON.stringify({ version: 1, theme: "contrast", language: null, apiOrigin: "http://example.com", launchAtLogin: "yes" });
  assert.deepEqual(readPreferences({ getItem: () => stored }), defaultPreferences);
  const valid = JSON.stringify({ version: 1, theme: ThemePreference.Dark, language: LanguagePreference.Korean, apiOrigin: "http://127.0.0.1:46307/", launchAtLogin: true });
  assert.deepEqual(readPreferences({ getItem: () => valid }), { version: 1, theme: ThemePreference.Dark, language: LanguagePreference.Korean, apiOrigin: "http://127.0.0.1:46307/", launchAtLogin: true });
  assert.equal(isValidApiOrigin("https://devhud.api.delino.io/"), true);
  assert.equal(isValidApiOrigin("https://devhud.api.delino.io/path"), false);
});

test("describes Korean diagnostics as redacted rather than deleted", () => {
  assert.match(messages.ko.diagnosticsSummary, /민감 정보가 삭제된/u);
});

test("uses the first supported platform language", () => {
  assert.equal(selectSupportedLanguage(["fr-FR", "ko-KR", "en-US"]), "ko");
  assert.equal(selectSupportedLanguage(["fr-FR", "en-US", "ko-KR"]), "en");
});

test("falls back to English when no platform language is supported", () => {
  assert.equal(selectSupportedLanguage(["fr-FR"]), "en");
  assert.equal(selectSupportedLanguage([]), "en");
});

test("keeps first-run completion separate from versioned preferences", () => {
  const storage = new Map();
  const localStorage = { getItem: (key) => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value) };
  assert.equal(hasCompletedOnboarding(localStorage), false);
  completeOnboarding(localStorage);
  assert.equal(hasCompletedOnboarding(localStorage), true);
  assert.deepEqual(readPreferences(localStorage), defaultPreferences);
});
