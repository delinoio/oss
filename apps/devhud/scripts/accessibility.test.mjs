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

test("command palette uses buttons and has a localized empty state", () => {
  assert.doesNotMatch(app, /role="listbox"|role="option"/u);
  assert.match(app, /actions\.length === 0/u);
});

test("command palette shortcut is unavailable during onboarding", () => {
  assert.match(app, /!onboarding && \(event\.metaKey \|\| event\.ctrlKey\)/u);
});

test("first run renders the localized local-choice controls and focuses the API origin", () => {
  assert.match(app, /onboarding/u);
  assert.match(app, /autoFocus value=\{preferences\.apiOrigin\}/u);
  assert.match(app, /copy\.signIn/u);
  assert.match(app, /copy\.continueLocally/u);
  assert.match(styles, /\.app-shell\.onboarding\s*\{\s*grid-template-columns:minmax\(0,1fr\)/u);
});
