import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { load as loadYaml } from "js-yaml";

import { assertOverlayCopies, configureIosWidgetProject } from "./generate-mobile.mjs";

test("materialized mobile projects require every expected overlay", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-mobile-overlays-"));
  const source = join(root, "source.xml");
  const destination = join(root, "generated/destination.xml");
  writeFileSync(source, "expected");
  try {
    assert.doesNotThrow(() => assertOverlayCopies([{ source, destination }], false, root));
    assert.throws(() => assertOverlayCopies([{ source, destination }], true, root), /overlay is missing/u);
    mkdirSync(join(destination, ".."), { recursive: true });
    writeFileSync(destination, "stale");
    assert.throws(() => assertOverlayCopies([{ source, destination }], true, root), /overlay is stale/u);
    writeFileSync(destination, "expected");
    assert.doesNotThrow(() => assertOverlayCopies([{ source, destination }], true, root));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("generated iOS projects embed the production widget extension", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-ios-widget-"));
  const project = join(root, "project.yml");
  writeFileSync(project, "name: DevHUD\ntargets:\n  DevHUD_iOS:\n    type: application\n    platform: iOS\n    info:\n      properties:\n        CFBundleVersion: 42\n        CFBundleShortVersionString: 2.3.4\n");
  try {
    configureIosWidgetProject(project);
    const generated = readFileSync(project, "utf8");
    const parsed = loadYaml(generated);
    assert.match(generated, /PRODUCT_BUNDLE_IDENTIFIER: io\.delino\.devhud\.widget/u);
    assert.match(generated, /PRODUCT_BUNDLE_IDENTIFIER: io\.delino\.devhud\.widget\.intent/u);
    assert.match(generated, /target: DevHudWidget[\s\S]*embed: true/u);
    assert.match(generated, /target: DevHudWidgetIntent[\s\S]*embed: true/u);
    assert.match(generated, /deploymentTarget: '16\.0'/u);
    assert.match(generated, /INFOPLIST_FILE: DevHudWidget\/Info\.plist/u);
    assert.match(generated, /INFOPLIST_FILE: DevHudWidgetIntent\/Info\.plist/u);
    assert.match(generated, /CODE_SIGN_ENTITLEMENTS: DevHudWidget\/DevHudWidget\.entitlements/u);
    assert.match(generated, /CODE_SIGN_ENTITLEMENTS: DevHudWidgetIntent\/DevHudWidgetIntent\.entitlements/u);
    assert.match(generated, /path: DevHudWidgetShared\/en\.lproj/u);
    assert.match(generated, /path: DevHudWidgetShared\/ko\.lproj/u);
    for (const target of ["DevHudWidget", "DevHudWidgetIntent"]) {
      assert.equal(parsed.targets[target].info, undefined);
      assert.equal(parsed.targets[target].entitlements, undefined);
      assert.equal(parsed.targets[target].settings.base.CURRENT_PROJECT_VERSION, 42);
      assert.equal(parsed.targets[target].settings.base.MARKETING_VERSION, "2.3.4");
    }
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test("generated iOS widget targets require application-owned versions", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-ios-widget-version-"));
  const project = join(root, "project.yml");
  writeFileSync(project, "name: DevHUD\ntargets:\n  DevHUD_iOS:\n    type: application\n    platform: iOS\n    info:\n      properties:\n        CFBundleVersion: 42\n");
  try {
    assert.throws(() => configureIosWidgetProject(project), /marketing version/u);
  } finally { rmSync(root, { recursive: true, force: true }); }
});
