import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const scriptsDirectory = dirname(fileURLToPath(import.meta.url));
const desktopHost = readFileSync(join(scriptsDirectory, "../src-tauri/src/main.rs"), "utf8");
const mobileHost = readFileSync(join(scriptsDirectory, "../src-tauri/src/lib.rs"), "utf8");
const nativePlugin = readFileSync(join(scriptsDirectory, "../src-tauri/src/native_plugin.rs"), "utf8");
const tauriConfig = JSON.parse(readFileSync(join(scriptsDirectory, "../src-tauri/tauri.conf.json"), "utf8"));

function segment(source, start, end) {
  const startIndex = source.indexOf(start);
  assert.notEqual(startIndex, -1, `missing ${start}`);
  const endIndex = source.indexOf(end, startIndex);
  assert.notEqual(endIndex, -1, `missing ${end}`);
  return source.slice(startIndex, endIndex + end.length);
}

function restoredWindowOperations(source, marker) {
  return segment(source, marker, "\n}\n\n");
}

test("desktop CEF main window is natively maximized only at creation", () => {
  const builder = segment(
    desktopHost,
    "let webview = tauri::WebviewWindowBuilder::<tauri::Cef, _>::new(",
    ".build()?;",
  );
  const maximized = builder.indexOf(".maximized(true)");
  const build = builder.indexOf(".build()?");

  assert(maximized >= 0, "desktop main window must request native maximization");
  assert(maximized < build, "desktop maximization must be configured before build");
  assert.match(builder, /\.inner_size\(960\.0, 640\.0\)/u);
  assert.match(builder, /\.min_inner_size\(640\.0, 480\.0\)/u);
  assert.doesNotMatch(builder, /\.fullscreen\(/u);
});

test("configuration preserves the desktop restored and minimum dimensions", () => {
  const mainWindow = tauriConfig.app.windows.find((window) => window.label === "main");

  assert.deepEqual(mainWindow, {
    label: "main",
    title: "DevHUD",
    create: false,
    width: 960,
    height: 640,
    minWidth: 640,
    minHeight: 480,
    devtools: false,
  });
});

test("desktop restoration and mobile creation never re-maximize", () => {
  for (const operations of [
    restoredWindowOperations(desktopHost, "fn restore_main_window<R: tauri::Runtime>"),
    restoredWindowOperations(nativePlugin, "fn restore_main_window<R: Runtime>"),
  ]) {
    assert.match(operations, /window\.unminimize\(\)/u);
    assert.match(operations, /window\.show\(\)/u);
    assert.match(operations, /window\.set_focus\(\)/u);
    assert.doesNotMatch(operations, /(?:maximized|fullscreen|inner_size|min_inner_size|set_size|set_position)/u);
  }

  const mobileBuilder = segment(
    mobileHost,
    "tauri::WebviewWindowBuilder::new(",
    ".build()?;",
  );
  assert.doesNotMatch(mobileBuilder, /(?:maximized|fullscreen)/u);
});
