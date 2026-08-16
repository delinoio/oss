import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const appRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
const styles = readFileSync(join(appRoot, "src/styles.css"), "utf8");
const app = readFileSync(join(appRoot, "src/App.tsx"), "utf8");
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
  assert.match(app, /rightModifier\.current === "ControlRight"/u);
  assert.match(app, /rightModifier\.current === "MetaRight"/u);
  assert.match(app, /event\.location === rightModifierLocation/u);
  assert.match(app, /addEventListener\("keyup", releaseRightModifier\)/u);
  assert.match(app, /!onboarding && matchingRightModifier/u);
  assert.match(app, /copy\.rightCommandK : copy\.rightControlK/u);
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

test("Account focuses its API origin input when the surface opens", () => {
  assert.match(app, /const apiOriginInput = useRef<HTMLInputElement>\(null\);/u);
  assert.match(app, /surface === SurfaceId\.Account\) apiOriginInput\.current\?\.focus\(\)/u);
  assert.match(app, /<input ref=\{apiOriginInput\} value=\{preferences\.apiOrigin\}/u);
});
