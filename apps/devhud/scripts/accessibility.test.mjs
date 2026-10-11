import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

// Source assertions allow formatter-owned whitespace and trailing commas while
// retaining the required selectors, values, handlers and accessibility attributes.
const appRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
const styles = readFileSync(join(appRoot, "src/styles.css"), "utf8");
const app = readFileSync(join(appRoot, "src/App.tsx"), "utf8");
const diagnosticsUi = readFileSync(
  join(appRoot, "src/diagnostics-ui.tsx"),
  "utf8",
);
const identityUi = readFileSync(join(appRoot, "src/identity-ui.tsx"), "utf8");
const foundation = readFileSync(join(appRoot, "src/ui-foundation.tsx"), "utf8");
const deckUi = readFileSync(join(appRoot, "src/deck-ui.tsx"), "utf8");
const icons = readFileSync(join(appRoot, "src/ui-icons.tsx"), "utf8");
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
  assert(
    contrastRatio("#ffffff", customColor(darkTheme, "--button-accent")) >= 4.5,
  );
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
  assert.match(
    styles,
    /\.external-message\[role="alert"\]\s*\s*\{\s*\s*color:\s*var\(\s*--error\s*\)/u,
  );
  assert.match(
    styles,
    /\.native-setting-error\s*\s*\{\s*[^}]*color:\s*var\(\s*--error\s*\)/u,
  );
});

test("RealQA panels use the defined themed surface color", () => {
  assert.doesNotMatch(styles, /var\(\s*--panel\s*\)/u);
  assert.match(
    styles,
    /\.draft-list\s*>\s*li\s*\{\s*[^}]*background:\s*var\(\s*--surface\s*\)/u,
  );
  assert.match(
    styles,
    /\.floating-capture-preview\s*\{\s*[^}]*background:\s*var\(\s*--surface\s*\)/u,
  );
  assert.match(
    styles,
    /\.capture-dialog\s*\{\s*[^}]*background:\s*var\(\s*--surface\s*\)/u,
  );
  assert.match(
    styles,
    /\.editor-controls\s*\{\s*[^}]*background:\s*var\(\s*--surface\s*\)/u,
  );
});

test("RealQA annotation text uses CSP-compatible static font styling", () => {
  assert.match(
    styles,
    /\.annotation-text\s*\{\s*font-family:\s*"DevHud\s*RealQA\s*Noto\s*Sans\s*KR";\s*font-kerning:\s*none;?\s*\}/u,
  );
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

test("Diagnostics disclosures preserve exact text selection and fit contracted responsive viewports", () => {
  assert.match(
    diagnosticsUi,
    /<details\s*className="diagnostics-disclosure"\s*open>/u,
  );
  assert.match(
    diagnosticsUi,
    /<details\s*className="diagnostics-disclosure">/u,
  );
  assert.match(
    diagnosticsUi,
    /<section\s*key=\{\s*previewGeneration\s*\}\s*className="diagnostics-disclosures"/u,
  );
  assert.match(
    diagnosticsUi,
    /<pre\s*className="diagnostics-preview"[^>]*tabIndex=\{\s*0\s*\}\s*>\s*\{\s*bundle\.requestJson\s*\}\s*<\/pre>/u,
  );
  assert.match(
    diagnosticsUi,
    /<pre\s*className="diagnostics-preview"[^>]*tabIndex=\{\s*0\s*\}\s*>\s*\{\s*bundle\.exportJson\s*\}\s*<\/pre>/u,
  );
  assert.match(
    styles,
    /\.diagnostics-disclosure\s*\s*>\s*\s*summary\s*\s*\{\s*[^}]*display:\s*list-item/u,
  );
  assert.match(
    styles,
    /\.diagnostics-preview\s*\s*\{\s*[^}]*max-height:\s*min\(\s*420px,\s*45vh\s*\);\s*[^}]*overflow:\s*auto;\s*[^}]*font-family:\s*ui-monospace[^}]*white-space:\s*pre;\s*[^}]*overflow-wrap:\s*normal;\s*[^}]*user-select:\s*text/u,
  );
  assert.match(
    styles,
    /\.diagnostics-preview:\s*focus-visible\s*\s*\{\s*[^}]*outline:\s*3px\s*solid\s*var\(\s*--focus\s*\)/u,
  );
  assert.match(
    styles,
    /\.diagnostics-runtime\s*\s*\{\s*[^}]*grid-template-columns:\s*repeat\(\s*3,\s*minmax\(\s*0,\s*1fr\s*\)\s*\)/u,
  );
  assert.match(
    styles,
    /\.runtime-diagnostics\s*\s*\{\s*[^}]*grid-template-columns:\s*max-content\s*minmax\(\s*0,\s*1fr\s*\);\s*[^}]*max-width:\s*510px/u,
  );
  assert.match(
    styles,
    /\.runtime-diagnostics\s*dt\s*\s*\{\s*[^}]*font-weight:\s*800/u,
  );
  assert.match(
    styles,
    /\.runtime-diagnostics\s*dd\s*\s*\{\s*[^}]*overflow-wrap:\s*anywhere/u,
  );
  assert.match(
    styles,
    /@media\s*\(\s*max-width:\s*700px\s*\)\s*\s*\{\s*[\s\S]*?\.diagnostics-runtime\s*\s*\{\s*\s*grid-template-columns:\s*minmax\(\s*0,\s*1fr\s*\)/u,
  );
});

test("command palette uses buttons and has a localized empty state", () => {
  assert.doesNotMatch(app, /role="listbox"|role="option"/u);
  assert.match(app, /actions\.length\s*===\s*0/u);
});

test("command palette shortcut is unavailable during onboarding", () => {
  assert.doesNotMatch(app, /MetaRight|ControlRight|event\.code === "KeyK"/u);
  assert.match(app, /event\.kind\s*===\s*"shortcut-triggered"/u);
  assert.match(
    app,
    /if\s*\(\s*context\.mobile\s*\|\|\s*context\.onboarding,?\s*\)\s*return;\s*/u,
  );
  assert.match(app, /event\.action\s*===\s*ShortcutActionId\.CommandPalette/u);
  assert.match(
    app,
    /actionRegistry\.find\(\s*\(\s*candidate,?\s*\)\s*=>\s*candidate\.id\s*===\s*event\.action,?\s*\)/u,
  );
  assert.match(app, /ShortcutPaletteTrigger/u);
  assert.match(
    identityUi,
    /binding\.enabled\s*\?\s*\[\s*\.\.\.modifiers,\s*\s*copy\[\s*shortcutKeyLabels\[\s*binding\.key,?\s*\],?\s*\],?\s*\]\.join/u,
  );
});

test("document preferences are synchronized before the first localized render", () => {
  assert.match(
    main,
    /synchronizeDocumentPreferences\(\s*document\.documentElement,\s*\s*preferences,\s*\s*matchMedia\(\s*"\(\s*prefers-color-scheme:\s*dark,?\s*\)",?\s*\)\.matches,\s*\s*navigator\.languages,?\s*\);\s*\s*createRoot\(\s*root,?\s*\)\.render/u,
  );
});

test("system language changes synchronize the rendered copy, document language, and tray", () => {
  assert.match(
    app,
    /const\s*\[\s*systemLanguage,\s*\s*setSystemLanguage,?\s*\]\s*=\s*useState\(\s*\(\s*,?\s*\)\s*=>\s*resolveLanguage\(\s*LanguagePreference\.System,\s*\s*navigator\.languages,?\s*\),?\s*\);\s*/u,
  );
  assert.match(
    app,
    /addEventListener\(\s*"languagechange",\s*\s*updateSystemLanguage,?\s*\);\s*/u,
  );
  assert.match(
    app,
    /removeEventListener\(\s*"languagechange",\s*\s*updateSystemLanguage,?\s*\);\s*/u,
  );
  assert.match(
    app,
    /preferences\.language\s*===\s*LanguagePreference\.System\s*\?\s*systemLanguage\s*:\s*preferences\.language/u,
  );
  assert.match(
    app,
    /\s*\},\s*\s*\[\s*preferences\.language,\s*\s*preferences\.theme,\s*\s*language,?\s*\],?\s*\);\s*/u,
  );
});

test("resolved themes apply their native control color scheme", () => {
  assert.match(themeBlocks[0], /color-scheme:\s*light/u);
  assert.match(themeBlocks[1], /color-scheme:\s*dark/u);
});

test("the palette trigger retains contrast while hovered", () => {
  assert.match(app, /className="palette-trigger"/u);
  assert.match(
    styles,
    /\.shell-nav-item:\s*hover,\s*\.shell-navigation\s*\.palette-trigger:\s*hover\s*\s*\{\s*[^}]*border-color:\s*var\(\s*--line\s*\);\s*[^}]*background:\s*var\(\s*--surface-muted\s*\)/u,
  );
});

test("external status remains localized and noopener does not report a false failure", () => {
  const shell = readFileSync(join(appRoot, "src/shell.ts"), "utf8");
  assert.match(
    app,
    /type\s*ExternalMessage\s*=\s*"opened"\s*\|\s*"failed"\s*\|\s*"invalid-api-origin"/u,
  );
  assert.match(
    app,
    /const\s*externalMessageText\s*=\s*externalMessage\s*===\s*"invalid-api-origin"/u,
  );
  assert.doesNotMatch(
    shell,
    /ExternalLinkTarget\.Documentation|tree\/main\/docs/u,
  );
  assert.doesNotMatch(shell, /if \(!window\.open/u);
  assert.match(
    shell,
    /window\.open\(\s*path,\s*\s*"_blank",\s*\s*"noopener,\s*noreferrer",?\s*\);\s*/u,
  );
});

test("first run renders the localized local-choice controls and focuses the API origin", () => {
  assert.match(app, /onboarding/u);
  assert.match(identityUi, /value=\{\s*apiOrigin\s*\}\s*autoFocus\s*onApply/u);
  assert.match(identityUi, /copy\.signIn/u);
  assert.match(identityUi, /copy\.continueLocally/u);
  assert.match(identityUi, /copy\.customApiWarning/u);
  assert.match(
    app,
    /if\s*\(\s*onboarding,?\s*\)\s*return\s*boundary\(\s*<main\s*className="standalone-shell"/u,
  );
  assert.match(identityUi, /<Card\s*className="onboarding-card">/u);
});

test("Account focuses its API origin input when the surface opens or is reselected from the palette", () => {
  assert.match(
    app,
    /const\s*apiOriginInput\s*=\s*useRef<HTMLInputElement>\(\s*null,?\s*\);\s*/u,
  );
  assert.match(
    app,
    /surface\s*===\s*SurfaceId\.Account,?\s*\)\s*apiOriginInput\.current\?\.focus\(\s*,?\s*\)/u,
  );
  assert.match(app, /inputRef=\{\s*apiOriginInput\s*\}/u);
  assert.match(
    identityUi,
    /<input\s*id=\{\s*inputId\s*\}\s*ref=\{\s*inputRef\s*\}\s*autoFocus=\{\s*autoFocus\s*\}/u,
  );
  assert.match(
    app,
    /closePalette\(\s*action\?\.surface\s*!==\s*SurfaceId\.Account\s*&&\s*!opensCaptureDialog,?\s*\);\s*/u,
  );
  assert.match(
    app,
    /action\?\.surface\s*===\s*SurfaceId\.Account,?\s*\)\s*requestAnimationFrame\(\s*\(\s*,?\s*\)\s*=>\s*apiOriginInput\.current\?\.focus\(\s*,?\s*\),?\s*\)/u,
  );
});

test("Account opener actions and invalidating edits ignore stale external completions", () => {
  assert.match(app, /const\s*externalAttempt\s*=\s*useRef\(\s*0,?\s*\);\s*/u);
  assert.match(
    app,
    /if\s*\(\s*"apiOrigin"\s*in\s*next,?\s*\)\s*\{\s*\s+externalAttempt\.current\s*\+=\s*1;\s*\s+setExternalMessage\(\s*null,?\s*\);\s*/u,
  );
  assert.match(
    app,
    /const\s*external\s*=\s*async\s*\(\s*target:\s*ExternalLinkTarget,?\s*\)\s*=>\s*\{\s*\s+const\s*attempt\s*=\s*externalAttempt\.current\s*\+\s*1;\s*\s+externalAttempt\.current\s*=\s*attempt;\s*/u,
  );
  assert.match(
    app,
    /if\s*\(\s*attempt\s*===\s*externalAttempt\.current,?\s*\)\s*setExternalMessage\(\s*message,?\s*\);\s*/u,
  );
  assert.match(
    app,
    /const\s*finishOnboarding\s*=\s*\(\s*,?\s*\)\s*=>\s*\{\s*\s+externalAttempt\.current\s*\+=\s*1;\s*\s+setExternalMessage\(\s*null,?\s*\);\s*/u,
  );
  assert.match(
    app,
    /clearIdentityForApiChange\(\s*bridge,\s*\s*storage,\s*\s*preferences\.apiOrigin,\s*\s*identitySession,?\s*\)/u,
  );
  assert.match(identityUi, /identity\.signIn\(\s*,?\s*\)/u);
});

test("RealQA exposes unsupported capture actions as disabled controls", () => {
  assert.match(
    app,
    /const\s*unavailableCaptureActions\s*=\s*actionRegistry\.filter/u,
  );
  assert.match(
    app,
    /action\.required\.includes\(\s*PlatformCapability\.Capture,?\s*\)/u,
  );
  assert.match(
    app,
    /<div\s*className="disabled-actions">\s*\{\s*unavailableCaptureActions\.map\(\s*\(\s*action,?\s*\)\s*=>\s*(?:\(\s*)?<button\s*disabled/u,
  );
});

test("tray localization failures are caught and recorded", () => {
  const shell = readFileSync(join(appRoot, "src/shell.ts"), "utf8");
  assert.match(shell, /\?\?\s*Promise\.resolve\(\s*,?\s*\)/u);
  assert.match(
    app,
    /void\s*setTrayLanguage\(\s*language,?\s*\)\.catch\(\s*\(\s*,?\s*\)\s*=>\s*\{\s*\s*\},?\s*\);\s*/u,
  );
  assert.match(nativeHost, /event = "tray_language_update_failed"/u);
});

test("rejected external destinations emit a safe structured diagnostic", () => {
  assert.match(nativeHost, /event = "external_destination_rejected"/u);
  assert.doesNotMatch(
    nativeHost,
    /external_destination_rejected[^\n]*api_origin/u,
  );
});

test("Linux external links use bounded GIO dispatch rather than browser lifetime", () => {
  assert.match(nativeHost, /gio::AppInfo::launch_default_for_uri/u);
  assert.match(
    nativeHost,
    /receiver\.recv_timeout\(EXTERNAL_OPENER_TIMEOUT\)/u,
  );
  assert.doesNotMatch(nativeHost, /Command::new\("xdg-open"\)/u);
});

test("internal overlays stack above every responsive navigation surface", () => {
  assert.match(styles, /\.ui-overlay\s*\s*\{\s*[^}]*z-index:\s*30/u);
  assert.match(styles, /\.shell-navigation\s*\s*\{\s*[^}]*z-index:\s*10/u);
  assert.match(styles, /\.mobile-app-bar\s*\s*\{\s*[^}]*z-index:\s*10/u);
  assert.match(styles, /\.app-shell-bottom-bar\s*\s*\{\s*[^}]*z-index:\s*10/u);
});

test("the UI foundation encodes the semantic, sizing, and responsive contracts", () => {
  for (const block of themeBlocks) {
    for (const token of [
      "--bg",
      "--surface",
      "--surface-muted",
      "--text",
      "--muted",
      "--accent",
      "--success",
      "--warning",
      "--error",
      "--info",
      "--line",
      "--focus",
    ]) {
      assert.match(block, new RegExp(`${token}:`, "u"));
    }
  }
  assert.match(themeBlocks[0], /--space-1:\s*8px/u);
  assert.match(themeBlocks[0], /--radius-control:\s*12px/u);
  assert.match(themeBlocks[0], /--radius-panel:\s*16px/u);
  assert.match(themeBlocks[0], /--target-min:\s*44px/u);
  assert.match(
    styles,
    /button\s*\s*\{\s*[^}]*min-width:\s*var\(\s*--target-min\s*\);\s*[^}]*min-height:\s*var\(\s*--target-min\s*\)/u,
  );
  assert.match(
    styles,
    /\.ui-card-interactive\s*\s*>\s*\s*button\.data-row:\s*focus-visible\s*\s*\{\s*[^}]*outline-offset:\s*-4px/u,
  );
  assert.match(
    styles,
    /grid-template-columns:\s*232px\s*minmax\(\s*0,\s*1fr\s*\)/u,
  );
  assert.match(
    styles,
    /grid-template-columns:\s*72px\s*minmax\(\s*0,\s*1fr\s*\)/u,
  );
  assert.match(
    styles,
    /\.shell-navigation\s*nav\s*\s*\{\s*[^}]*min-height:\s*0;\s*[^}]*overflow-y:\s*auto/u,
  );
  assert.match(
    styles,
    /\.shell-navigation\s*\s*>\s*\s*\.palette-trigger\s*\s*\{\s*[^}]*margin-top:\s*auto/u,
  );
  assert.match(
    styles,
    /\.content\s*\s*\{\s*[^}]*min-width:\s*0;\s*[^}]*max-width:\s*1280px/u,
  );
  assert.match(
    styles,
    /--mobile-bottom-navigation-height:\s*calc\(\s*64px\s*\+\s*env\(\s*safe-area-inset-bottom\s*\)\s*\)/u,
  );
  assert.match(
    styles,
    /padding:\s*[^;}]*calc\(\s*var\(\s*--mobile-bottom-navigation-height\s*\)\s*\+\s*var\(\s*--space-3\s*\)\s*\)/u,
  );
  assert.match(
    styles,
    /@media\s*\(\s*min-width:\s*701px\s*\)\s*and\s*\(\s*max-width:\s*1023px\s*\)/u,
  );
  assert.match(styles, /@media\s*\(\s*max-width:\s*700px\s*\)/u);
});

test("reduced motion preserves visibility transforms", () => {
  const reducedMotion = styles.match(
    /@media \(prefers-reduced-motion: reduce\)\s*\{([\s\S]*?)\}/u,
  )?.[1];
  assert.ok(reducedMotion);
  assert.match(reducedMotion, /transition:\s*none\s*!important/u);
  assert.match(reducedMotion, /animation:\s*none\s*!important/u);
  assert.doesNotMatch(reducedMotion, /transform:\s*none/u);
});

test("narrow layouts keep capture previews above the safe-area-aware navigation", () => {
  assert.match(
    styles,
    /\.floating-capture-preview\s*\{\s*[^}]*bottom:\s*1rem[^}]*;?\s*\}[\s\S]*@media\s*\(\s*max-width:\s*700px\s*\)\s*\{\s*\.floating-capture-preview\s*\{\s*bottom:\s*calc\(\s*var\(\s*--mobile-bottom-navigation-height,\s*\s*calc\(\s*64px\s*\+\s*env\(\s*safe-area-inset-bottom\s*\)\s*\)\s*\)\s*\+\s*1rem\s*\);?\s*\};?\s*\}/u,
  );
});

test("Deck configuration scrolls inside a viewport-bounded sticky panel", () => {
  assert.match(
    styles,
    /\.deck-configuration-panel\s*\s*\{\s*[^}]*position:\s*sticky;\s*[^}]*top:\s*var\(\s*--space-2\s*\);\s*[^}]*max-block-size:\s*calc\(\s*100vh\s*-\s*var\(\s*--space-2\s*\)\s*-\s*var\(\s*--space-2\s*\)\s*\);\s*[^}]*overflow-y:\s*auto;\s*/u,
  );
});

test("constrained Deck results place status badges below their content", () => {
  assert.match(
    styles,
    /\.deck-workspace\s*\s*\{\s*[^}]*container-type:\s*inline-size;\s*/u,
  );
  assert.match(
    styles,
    /@container\s*\(\s*max-width:\s*40rem\s*\)\s*\s*\{\s*\s*\.deck-results\s*\.data-row\s*\s*\{\s*[^}]*grid-template-columns:\s*auto\s*minmax\(\s*0,\s*1fr\s*\)[^}]*;?\s*\}[^}]*\.deck-results\s*\.data-row-trailing\s*\s*\{\s*[^}]*grid-column:\s*2;\s*[^}]*justify-content:\s*start/u,
  );
  assert.match(
    styles,
    /\.deck-result-statuses\s*\.status-badge\s*\s*\{\s*[^}]*max-width:\s*100%;\s*[^}]*white-space:\s*normal;\s*[^}]*overflow-wrap:\s*anywhere;\s*/u,
  );
});

test("mobile Deck selection can shrink with enlarged text", () => {
  assert.match(
    styles,
    /@media\s*\(\s*max-width:\s*\s*700px\s*\)\s*\s*\{\s*[\s\S]*?\.deck-workspace-controls\s*\.ui-field\s*\s*\{\s*[^}]*flex:\s*1\s*1\s*100%;\s*[^}]*min-width:\s*0;\s*/u,
  );
});

test("rail and lower-sidebar Deck headers stack and constrain their actions", () => {
  assert.match(
    styles,
    /@media\s*\(\s*min-width:\s*701px\s*\)\s*and\s*\(\s*max-width:\s*1023px\s*\)\s*\s*\{\s*[^}]*\.deck\s*\s*>\s*\s*\.page-header\s*\s*\{\s*\s*display:\s*block;\s*\s*;?\s*\}[^}]*\.deck\s*\s*>\s*\s*\.page-header\s*\.page-header-actions\s*\s*\{\s*[^}]*min-width:\s*0;\s*[^}]*width:\s*100%;\s*[^}]*;?\s*\}[^}]*\.deck-workspace-controls\s*\s*\{\s*[^}]*justify-content:\s*start;\s*[^}]*min-width:\s*0;\s*[^}]*;?\s*\}[^}]*\.deck-workspace-controls\s*\.ui-field\s*\s*\{\s*[^}]*flex:\s*1\s*1\s*11rem;\s*[^}]*max-width:\s*100%;\s*/u,
  );
  assert.match(
    styles,
    /\.deck\s*\s*\{\s*\s*container-type:\s*inline-size;\s*\s*;?\s*\}/u,
  );
  assert.match(
    styles,
    /@container\s*\(\s*max-width:\s*64rem\s*\)\s*\s*\{\s*[^}]*\.deck\s*\s*>\s*\s*\.page-header\s*\s*\{\s*\s*display:\s*block;\s*\s*;?\s*\}[^}]*\.deck\s*\s*>\s*\s*\.page-header\s*\.page-header-actions\s*\s*\{\s*[^}]*min-width:\s*0;\s*[^}]*width:\s*100%;\s*[^}]*;?\s*\}[^}]*\.deck-workspace-controls\s*\s*\{\s*[^}]*justify-content:\s*start;\s*[^}]*min-width:\s*0;\s*[^}]*;?\s*\}[^}]*\.deck-workspace-controls\s*\.ui-field\s*\s*\{\s*[^}]*flex:\s*1\s*1\s*11rem;\s*[^}]*max-width:\s*100%;\s*/u,
  );
});

test("the shell exposes a localized skip target and named rail tooltips", () => {
  assert.match(
    foundation,
    /className="skip-link"\s*href="#devhud-main-content"/u,
  );
  assert.match(
    foundation,
    /id="devhud-main-content"\s*className="content"\s*tabIndex=\{\s*-1\s*\}/u,
  );
  assert.match(
    app,
    /aria-describedby=\{\s*compact\s*\?\s*tooltipId\s*:\s*undefined\s*\}/u,
  );
  assert.match(
    app,
    /className="nav-tooltip\s*rail-nav-tooltip"\s*role="tooltip"/u,
  );
  assert.match(app, /createPortal\(\s*[^;]*document\.body,?\s*\)/u);
  assert.match(
    styles,
    /\.rail-nav-tooltip\[data-visible="true"\]\s*\s*\{\s*[^}]*opacity:\s*1;\s*[^}]*visibility:\s*visible/u,
  );
});

test("measured shell geometry avoids CSP-blocked React style values", () => {
  assert.doesNotMatch(app, /style=\{tooltipPosition/u);
  assert.match(
    app,
    /tooltipElement\.style\.setProperty\(\s*"inset-block-start"/u,
  );
  assert.match(
    app,
    /tooltipElement\.style\.setProperty\(\s*"inset-inline-start"/u,
  );
  assert.doesNotMatch(foundation, /const shellStyle/u);
  assert.match(
    foundation,
    /shellElement\.style\.setProperty\(\s*"--mobile-bottom-navigation-height"/u,
  );
});

test("mobile dialogs subtract their safe-area-aware top offset from the viewport", () => {
  assert.match(
    styles,
    /\.ui-dialog\s*\s*\{\s*[^}]*max-height:\s*calc\(\s*100vh\s*-\s*56px\s*-\s*env\(\s*safe-area-inset-top\s*\)\s*\);\s*[^}]*margin-top:\s*calc\(\s*40px\s*\+\s*env\(\s*safe-area-inset-top\s*\)\s*\)/u,
  );
});

test("modal primitives own focus trapping, Escape, and opener restoration", () => {
  assert.match(
    foundation,
    /event\.key\s*===\s*"Escape"[\s\S]*onClose\(\s*,?\s*\)/u,
  );
  assert.match(
    foundation,
    /event\.shiftKey\s*&&\s*document\.activeElement\s*===\s*first[\s\S]*last\.focus\(\s*,?\s*\)/u,
  );
  assert.match(
    foundation,
    /document\.activeElement\s*===\s*last[\s\S]*first\.focus\(\s*,?\s*\)/u,
  );
  assert.match(
    foundation,
    /returnFocusRef\?\.current\s*\?\?\s*capturedOpener\.current/u,
  );
  assert.match(foundation, /role\s*=\s*"dialog"/u);
  assert.match(foundation, /role=\{\s*role\s*\}\s*aria-modal="true"/u);
});

test("Deck keeps configuration in the desktop panel and a named mobile sheet", () => {
  assert.match(
    deckUi,
    /!mobile\s*&&\s*(?:\(\s*)?<aside\s*className="deck-configuration-panel"\s*aria-label=\{\s*copy\.deckConfiguration\s*\}/u,
  );
  assert.match(
    deckUi,
    /mobile\s*&&\s*(?:\(\s*)?<Sheet\s*open=\{\s*settingsOpen\s*\}\s*inert=\{\s*widgetConfirmationOpen\s*\}\s*title=\{\s*isCreating\s*\?\s*copy\.deckCreate\s*:\s*copy\.deckConfiguration\s*\}\s*backLabel=\{\s*copy\.back\s*\}/u,
  );
  assert.match(
    deckUi,
    /backLabel=\{\s*copy\.back\s*\}\s*returnFocusRef=\{\s*sheetReturnFocus\s*\}\s*backButtonRef=\{\s*settingsSheetBackButton\s*\}\s*onClose=\{\s*closeSettings\s*\}/u,
  );
  assert.match(
    deckUi,
    /sheetReturnFocus\.current\s*=\s*createDeckButton\.current;\s*[\s\S]*setSettingsOpen\(\s*false,?\s*\)/u,
  );
  assert.doesNotMatch(deckUi, /returnFocusRef=\{settingsTrigger\}/u);
  assert.match(
    styles,
    /\.deck-workspace-layout\s*\s*\{\s*[^}]*grid-template-columns:\s*minmax\(\s*0,\s*1fr\s*\)\s*minmax\(\s*20rem,\s*24rem\s*\)/u,
  );
  assert.match(
    styles,
    /@media\s*\(\s*min-width:\s*701px\s*\)\s*and\s*\(\s*max-width:\s*1023px\s*\)\s*\s*\{\s*[\s\S]*?\.deck-workspace-layout\s*\s*\{\s*\s*grid-template-columns:\s*minmax\(\s*0,\s*1fr\s*\)\s*minmax\(\s*0,\s*min\(\s*24rem,\s*42%\s*\)\s*\);\s*\s*;?\s*\}/u,
  );
  assert.match(
    styles,
    /@media\s*\(\s*min-width:\s*1024px\s*\)\s*and\s*\(\s*max-width:\s*1279px\s*\)\s*\s*\{\s*[\s\S]*?\.deck-workspace-layout\s*\s*\{\s*\s*grid-template-columns:\s*minmax\(\s*0,\s*1fr\s*\)\s*minmax\(\s*0,\s*min\(\s*24rem,\s*42%\s*\)\s*\);\s*\s*;?\s*\}/u,
  );
  assert.match(
    styles,
    /\.deck-workspace-layout\s*\s*\{\s*\s*grid-template-columns:\s*minmax\(\s*0,\s*1fr\s*\);\s*\s*;?\s*\}/u,
  );
});

test("Deck checkbox labels retain the shared 44px interaction target", () => {
  assert.match(
    deckUi,
    /<label\s*className="check\s*deck-check"\s*htmlFor="deck-show-drafts">/u,
  );
  assert.match(
    deckUi,
    /<label\s*className="check\s*deck-check"\s*htmlFor="deck-notifications">/u,
  );
  assert.match(
    styles,
    /\.check\s*\s*\{\s*[^}]*min-height:\s*var\(\s*--target-min\s*\)/u,
  );
  assert.match(
    styles,
    /\.check\s*input\s*\s*\{\s*[^}]*width:\s*20px[^}]*min-height:\s*20px[^}]*height:\s*20px/u,
  );
});

test("foundation icons are repository-owned decorative SVGs", () => {
  assert.match(icons, /return\s*(?:\(\s*)?<svg/u);
  assert.match(icons, /aria-hidden="true"\s*focusable="false"/u);
  assert.doesNotMatch(icons, /from "(?!react")/u);
});

test("updater confirmations stack above the persistent capture preview", () => {
  assert.match(styles, /\.floating-capture-preview\s*\{\s*[^}]*z-index:\s*20/u);
  assert.match(
    styles,
    /\.updater-confirmation-backdrop\s*\{\s*[^}]*z-index:\s*21/u,
  );
});

test("capture dialogs reserve the overlay inset inside the viewport", () => {
  assert.match(
    styles,
    /\.overlay\s*\s*\{\s*[^}]*--overlay-top:\s*14vh;\s*[^}]*padding:\s*var\(\s*--overlay-top\s*\)\s*1rem\s*1rem/u,
  );
  assert.match(
    styles,
    /\.capture-dialog\s*\{\s*[^}]*max-height:\s*calc\(\s*100vh\s*-\s*var\(\s*--overlay-top\s*\)\s*-\s*1rem\s*\);\s*[^}]*overflow:\s*auto/u,
  );
});
