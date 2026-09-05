import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const appRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
const styles = readFileSync(join(appRoot, "src/styles.css"), "utf8");
const app = readFileSync(join(appRoot, "src/App.tsx"), "utf8");
const identityUi = readFileSync(join(appRoot, "src/identity-ui.tsx"), "utf8");
const foundation = readFileSync(join(appRoot, "src/ui-foundation.tsx"), "utf8");
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
  assert.match(styles, /\.native-setting-error\s*\{[^}]*color:var\(--error\)/u);
});

test("RealQA panels use the defined themed surface color", () => {
  assert.doesNotMatch(styles, /var\(--panel\)/u);
  assert.match(styles, /\.draft-list>li\{[^}]*background:var\(--surface\)/u);
  assert.match(styles, /\.floating-capture-preview\{[^}]*background:var\(--surface\)/u);
  assert.match(styles, /\.capture-dialog\{[^}]*background:var\(--surface\)/u);
  assert.match(styles, /\.editor-controls\{[^}]*background:var\(--surface\)/u);
});

test("RealQA annotation text uses CSP-compatible static font styling", () => {
  assert.match(styles, /\.annotation-text\{font-family:"DevHud RealQA Noto Sans KR";font-kerning:none\}/u);
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
  assert.doesNotMatch(app, /MetaRight|ControlRight|event\.code === "KeyK"/u);
  assert.match(app, /event\.kind === "shortcut-triggered"/u);
  assert.match(app, /if \(context\.mobile \|\| context\.onboarding\) return;/u);
  assert.match(app, /event\.action === ShortcutActionId\.CommandPalette/u);
  assert.match(app, /actionRegistry\.find\(\(candidate\) => candidate\.id === event\.action\)/u);
  assert.match(app, /ShortcutPaletteTrigger/u);
  assert.match(identityUi, /binding\.enabled \? \[\.\.\.modifiers, copy\[shortcutKeyLabels\[binding\.key\]\]\]\.join/u);
});

test("document preferences are synchronized before the first localized render", () => {
  assert.match(main, /synchronizeDocumentPreferences\(document\.documentElement, preferences, matchMedia\("\(prefers-color-scheme: dark\)"\)\.matches, navigator\.languages\);\s*createRoot\(root\)\.render/u);
});

test("system language changes synchronize the rendered copy, document language, and tray", () => {
  assert.match(app, /const \[systemLanguage, setSystemLanguage\] = useState\(\(\) => resolveLanguage\(LanguagePreference\.System, navigator\.languages\)\);/u);
  assert.match(app, /addEventListener\("languagechange", updateSystemLanguage\);/u);
  assert.match(app, /removeEventListener\("languagechange", updateSystemLanguage\);/u);
  assert.match(app, /preferences\.language === LanguagePreference\.System \? systemLanguage : preferences\.language/u);
  assert.match(app, /\}, \[preferences\.language, preferences\.theme, language\]\);/u);
});

test("resolved themes apply their native control color scheme", () => {
  assert.match(themeBlocks[0], /color-scheme:light/u);
  assert.match(themeBlocks[1], /color-scheme:dark/u);
});

test("the palette trigger retains contrast while hovered", () => {
  assert.match(app, /className="palette-trigger"/u);
  assert.match(styles, /\.shell-nav-item:hover,\.shell-navigation \.palette-trigger:hover\s*\{[^}]*border-color:var\(--line\);[^}]*background:var\(--surface-muted\)/u);
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
  assert.match(identityUi, /value=\{apiOrigin\} autoFocus onApply/u);
  assert.match(identityUi, /copy\.signIn/u);
  assert.match(identityUi, /copy\.continueLocally/u);
  assert.match(identityUi, /copy\.customApiWarning/u);
  assert.match(app, /if \(onboarding\) return boundary\(<main className="standalone-shell"/u);
  assert.match(identityUi, /<Card className="onboarding-card">/u);
});

test("Account focuses its API origin input when the surface opens or is reselected from the palette", () => {
  assert.match(app, /const apiOriginInput = useRef<HTMLInputElement>\(null\);/u);
  assert.match(app, /surface === SurfaceId\.Account\) apiOriginInput\.current\?\.focus\(\)/u);
  assert.match(app, /inputRef=\{apiOriginInput\}/u);
  assert.match(identityUi, /<input id=\{inputId\} ref=\{inputRef\} autoFocus=\{autoFocus\}/u);
  assert.match(app, /closePalette\(action\?\.surface !== SurfaceId\.Account\);/u);
  assert.match(app, /action\?\.surface === SurfaceId\.Account\) requestAnimationFrame\(\(\) => apiOriginInput\.current\?\.focus\(\)\)/u);
});

test("Account opener actions and invalidating edits ignore stale external completions", () => {
  assert.match(app, /const externalAttempt = useRef\(0\);/u);
  assert.match(app, /if \("apiOrigin" in next\) \{\s+externalAttempt\.current \+= 1;\s+setExternalMessage\(null\);/u);
  assert.match(app, /const external = async \(target: ExternalLinkTarget\) => \{\s+const attempt = externalAttempt\.current \+ 1;\s+externalAttempt\.current = attempt;/u);
  assert.match(app, /if \(attempt === externalAttempt\.current\) setExternalMessage\(message\);/u);
  assert.match(app, /const finishOnboarding = \(\) => \{\s+externalAttempt\.current \+= 1;\s+setExternalMessage\(null\);/u);
  assert.match(app, /clearIdentityForApiChange\(bridge, storage, preferences\.apiOrigin, identitySession\)/u);
  assert.match(identityUi, /identity\.signIn\(\)/u);
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

test("Linux external links use bounded GIO dispatch rather than browser lifetime", () => {
  assert.match(nativeHost, /gio::AppInfo::launch_default_for_uri/u);
  assert.match(nativeHost, /receiver\.recv_timeout\(EXTERNAL_OPENER_TIMEOUT\)/u);
  assert.doesNotMatch(nativeHost, /Command::new\("xdg-open"\)/u);
});

test("internal overlays stack above every responsive navigation surface", () => {
  assert.match(styles, /\.ui-overlay\s*\{[^}]*z-index:30/u);
  assert.match(styles, /\.shell-navigation\s*\{[^}]*z-index:10/u);
  assert.match(styles, /\.mobile-app-bar\s*\{[^}]*z-index:10/u);
  assert.match(styles, /\.app-shell-bottom-bar\s*\{[^}]*z-index:10/u);
});

test("the UI foundation encodes the semantic, sizing, and responsive contracts", () => {
  for (const block of themeBlocks) {
    for (const token of ["--bg", "--surface", "--surface-muted", "--text", "--muted", "--accent", "--success", "--warning", "--error", "--info", "--line", "--focus"]) {
      assert.match(block, new RegExp(`${token}:`, "u"));
    }
  }
  assert.match(themeBlocks[0], /--space-1:8px/u);
  assert.match(themeBlocks[0], /--radius-control:12px/u);
  assert.match(themeBlocks[0], /--radius-panel:16px/u);
  assert.match(themeBlocks[0], /--target-min:44px/u);
  assert.match(styles, /button\s*\{[^}]*min-width:var\(--target-min\);[^}]*min-height:var\(--target-min\)/u);
  assert.match(styles, /grid-template-columns:232px minmax\(0,1fr\)/u);
  assert.match(styles, /grid-template-columns:72px minmax\(0,1fr\)/u);
  assert.match(styles, /\.content\s*\{[^}]*min-width:0;[^}]*max-width:1280px/u);
  assert.match(styles, /--mobile-bottom-navigation-height:calc\(64px \+ env\(safe-area-inset-bottom\)\)/u);
  assert.match(styles, /padding:[^;}]*calc\(var\(--mobile-bottom-navigation-height\) \+ var\(--space-3\)\)/u);
  assert.match(styles, /@media \(min-width:701px\) and \(max-width:1023px\)/u);
  assert.match(styles, /@media \(max-width:700px\)/u);
});

test("reduced motion preserves visibility transforms", () => {
  const reducedMotion = styles.match(/@media \(prefers-reduced-motion: reduce\)\s*\{([^\n]*)\}/u)?.[1];
  assert.ok(reducedMotion);
  assert.match(reducedMotion, /transition:none!important/u);
  assert.match(reducedMotion, /animation:none!important/u);
  assert.doesNotMatch(reducedMotion, /transform:none/u);
});

test("narrow layouts keep capture previews above the safe-area-aware navigation", () => {
  assert.match(styles, /\.floating-capture-preview\{[^}]*bottom:1rem[^}]*\}[\s\S]*@media \(max-width:700px\)\{\.floating-capture-preview\{bottom:calc\(var\(--mobile-bottom-navigation-height, calc\(64px \+ env\(safe-area-inset-bottom\)\)\) \+ 1rem\)\}\}/u);
});

test("the shell exposes a localized skip target and named rail tooltips", () => {
  assert.match(foundation, /className="skip-link" href="#devhud-main-content"/u);
  assert.match(foundation, /id="devhud-main-content" className="content" tabIndex=\{-1\}/u);
  assert.match(app, /aria-describedby=\{shellLayout === ShellLayout\.Rail \? tooltipId : undefined\}/u);
  assert.match(app, /className="nav-tooltip" role="tooltip"/u);
  assert.match(styles, /button:focus-visible \.nav-tooltip\s*\{[^}]*opacity:1;[^}]*visibility:visible/u);
});

test("modal primitives own focus trapping, Escape, and opener restoration", () => {
  assert.match(foundation, /event\.key === "Escape"[\s\S]*onClose\(\)/u);
  assert.match(foundation, /event\.shiftKey && document\.activeElement === first[\s\S]*last\.focus\(\)/u);
  assert.match(foundation, /document\.activeElement === last[\s\S]*first\.focus\(\)/u);
  assert.match(foundation, /returnFocusRef\?\.current \?\? capturedOpener\.current/u);
  assert.match(foundation, /role="dialog" aria-modal="true"/u);
});

test("foundation icons are repository-owned decorative SVGs", () => {
  assert.match(icons, /return <svg/u);
  assert.match(icons, /aria-hidden="true" focusable="false"/u);
  assert.doesNotMatch(icons, /from "(?!react")/u);
});

test("updater confirmations stack above the persistent capture preview", () => {
  assert.match(styles, /\.floating-capture-preview\{[^}]*z-index:20/u);
  assert.match(styles, /\.updater-confirmation-backdrop\{[^}]*z-index:21/u);
});

test("capture dialogs reserve the overlay inset inside the viewport", () => {
  assert.match(styles, /\.overlay\s*\{[^}]*--overlay-top:14vh;[^}]*padding:var\(--overlay-top\) 1rem 1rem/u);
  assert.match(styles, /\.capture-dialog\{[^}]*max-height:calc\(100vh - var\(--overlay-top\) - 1rem\);[^}]*overflow:auto/u);
});
