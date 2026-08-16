import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const appRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
const styles = readFileSync(join(appRoot, "src/styles.css"), "utf8");
const app = readFileSync(join(appRoot, "src/App.tsx"), "utf8");
const main = readFileSync(join(appRoot, "src/main.tsx"), "utf8");
const nativeHost = readFileSync(join(appRoot, "src-tauri/src/main.rs"), "utf8");
const themeBlocks = [
  styles.match(/:root\s*\{([^}]*)\}/u)?.[1],
  styles.match(/html\[data-theme="dark"\]\s*\{([^}]*)\}/u)?.[1],
];

function customColor(block, name) {
  const match = block.match(new RegExp(`${name}:\\s*(#[a-f0-9]{6})`, "iu"));
  assert(match, `missing ${name} color`);
  return match[1];
}

function luminance(color) {
  const channels = color
    .slice(1)
    .match(/../gu)
    .map((channel) => Number.parseInt(channel, 16) / 255)
    .map((channel) =>
      channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4,
    );
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
}

function contrastRatio(foreground, background) {
  const lighter = Math.max(luminance(foreground), luminance(background));
  const darker = Math.min(luminance(foreground), luminance(background));
  return (lighter + 0.05) / (darker + 0.05);
}

test("eyebrow text meets WCAG AA contrast in light and dark themes", () => {
  assert.equal(themeBlocks.length, 2);
  for (const block of themeBlocks) {
    const eyebrow = customColor(block, "--devhud-eyebrow");
    const background = customColor(block, "--devhud-background");
    assert(
      contrastRatio(eyebrow, background) >= 4.5,
      `${eyebrow} does not have sufficient contrast against ${background}`,
    );
  }
});

test("active navigation buttons meet WCAG AA contrast in dark mode", () => {
  const darkTheme = themeBlocks[1];
  assert(contrastRatio("#ffffff", customColor(darkTheme, "--button-accent")) >= 4.5);
});

test("error messages meet WCAG AA contrast in light and dark themes", () => {
  for (const block of themeBlocks) {
    const error = customColor(block, "--error");
    const surface = customColor(block, "--surface");
    assert(
      contrastRatio(error, surface) >= 4.5,
      `${error} does not have sufficient contrast against ${surface}`,
    );
  }
  assert.match(styles, /\.external-message\[role="alert"\]\s*\{\s*color:var\(--error\)/u);
});

test("form-control boundaries meet non-text contrast in light and dark themes", () => {
  for (const block of themeBlocks) {
    const line = customColor(block, "--line");
    const surface = customColor(block, "--surface");
    assert(
      contrastRatio(line, surface) >= 3,
      `${line} does not provide a sufficient boundary against ${surface}`,
    );
  }
});

test("command palette uses buttons and has a localized empty state", () => {
  assert.doesNotMatch(app, /role="listbox"|role="option"/u);
  assert.match(app, /actions\.length === 0/u);
});

test("command palette shortcut is unavailable during onboarding", () => {
  assert.match(app, /const platformModifier = isMac \? "MetaRight" : "ControlRight"/u);
  assert.match(app, /rightModifier\.current === platformModifier/u);
  assert.match(app, /isMac \? event\.metaKey : event\.ctrlKey/u);
  assert.match(app, /const exactRightModifierChord = matchingRightModifier && !event\.shiftKey && !event\.altKey && \(isMac \? !event\.ctrlKey : !event\.metaKey\);/u);
  assert.match(app, /event\.location === rightModifierLocation/u);
  assert.match(app, /addEventListener\("keyup", releaseRightModifier\)/u);
  assert.match(app, /!onboarding && exactRightModifierChord/u);
  assert.match(app, /event\.code === "KeyK"/u);
  assert.match(app, /copy\.rightCommandK : copy\.rightControlK/u);
});

test("document preferences are synchronized before the first localized render", () => {
  assert.match(main, /synchronizeDocumentPreferences\(document\.documentElement, preferences, matchMedia\("\(prefers-color-scheme: dark\)"\)\.matches, navigator\.languages\); createRoot\(root\)\.render/u);
});

test("system-theme changes retain the current language preference", () => {
  assert.match(app, /\}, \[preferences\.language, preferences\.theme\]\);/u);
});

test("resolved themes apply their native control color scheme", () => {
  assert.match(themeBlocks[0], /color-scheme:light/u);
  assert.match(themeBlocks[1], /color-scheme:dark/u);
});

test("the palette trigger retains contrast while hovered", () => {
  assert.match(app, /className="palette-trigger"/u);
  assert.match(styles, /aside > \.palette-trigger:hover\s*\{\s*color:#fff; background:var\(--button-accent\);/u);
});

test("external status remains localized and noopener does not report a false failure", () => {
  const shell = readFileSync(join(appRoot, "src/shell.ts"), "utf8");
  assert.match(app, /type ExternalMessage = "opened" \| "failed" \| "invalid-api-origin"/u);
  assert.match(app, /const externalMessageText = externalMessage === "invalid-api-origin"/u);
  assert.doesNotMatch(shell, /ExternalLinkTarget\.Documentation|tree\/main\/docs/u);
  assert.doesNotMatch(shell, /if \(!window\.open/u);
  assert.match(shell, /window\.open\(path, "_blank", "noopener,noreferrer"\);/u);
});

test("first run renders the localized local-choice controls and focuses the API origin", () => {
  assert.match(app, /onboarding/u);
  assert.match(app, /autoFocus value=\{preferences\.apiOrigin\}/u);
  assert.match(app, /copy\.signIn/u);
  assert.match(app, /copy\.continueLocally/u);
  assert.match(styles, /\.app-shell\.onboarding\s*\{\s*grid-template-columns:minmax\(0,1fr\)/u);
});

test("Account focuses its API origin input when the surface opens or is reselected from the palette", () => {
  assert.match(app, /const apiOriginInput = useRef<HTMLInputElement>\(null\);/u);
  assert.match(app, /surface === SurfaceId\.Account\) apiOriginInput\.current\?\.focus\(\)/u);
  assert.match(app, /<input ref=\{apiOriginInput\} value=\{preferences\.apiOrigin\}/u);
  assert.match(app, /closePalette\(action\?\.surface !== SurfaceId\.Account\);/u);
  assert.match(app, /action\?\.surface === SurfaceId\.Account\) requestAnimationFrame\(\(\) => apiOriginInput\.current\?\.focus\(\)\)/u);
});

test("API-origin edits and local onboarding completion clear stale external messages", () => {
  assert.match(app, /const update = \(next: Partial<Preferences>\) => \{\s+if \("apiOrigin" in next\) setExternalMessage\(null\);/u);
  assert.match(app, /const finishOnboarding = \(\) => \{\s+setExternalMessage\(null\);/u);
});

test("RealQA exposes unsupported capture actions as disabled controls", () => {
  assert.match(app, /const unavailableCaptureActions = actionRegistry\.filter/u);
  assert.match(app, /action\.required\.includes\(PlatformCapability\.Capture\)/u);
  assert.match(app, /<div className="disabled-actions">\{unavailableCaptureActions\.map\(\(action\) => <button disabled/u);
});

test("tray localization failures are caught and recorded", () => {
  const shell = readFileSync(join(appRoot, "src/shell.ts"), "utf8");
  assert.match(shell, /\?\? Promise\.resolve\(\)/u);
  assert.match(app, /void setTrayLanguage\(language\)\.catch\(\(\) => \{\}\);/u);
  assert.match(nativeHost, /event = "tray_language_update_failed"/u);
});

test("rejected external destinations emit a safe structured diagnostic", () => {
  assert.match(nativeHost, /event = "external_destination_rejected"/u);
  assert.doesNotMatch(nativeHost, /external_destination_rejected[^\n]*api_origin/u);
});

test("command palette overlay stacks above the mobile sidebar", () => {
  assert.match(styles, /\.overlay\s*\{[^}]*z-index:2/u);
  assert.match(styles, /aside\s*\{[^}]*z-index:1/u);
});
