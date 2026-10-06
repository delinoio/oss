// SPDX-License-Identifier: Apache-2.0
import { execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { dryRunEnvironment } from "./bundle-macos-dry-run.mjs";

if (process.platform === "darwin") {
  const directory = mkdtempSync(join(tmpdir(), "delidev-widget-layout-"));
  try {
    const app = fileURLToPath(new URL("..", import.meta.url));
    const options = { cwd: app, env: dryRunEnvironment(process.env), stdio: "inherit" };
    const binary = join(directory, "widget-layout");
    // Optional fixture output is useful for visual review and never contains
    // native/account data. The default temporary images are removed below.
    const output = process.env.DELIDEV_WIDGET_LAYOUT_OUTPUT ?? join(directory, "images");
    mkdirSync(output, { recursive: true });
    execFileSync("xcrun", ["swiftc", "-parse-as-library", "macos-widget/Shared/Snapshot.swift", "macos-widget/Shared/Localization.swift", "macos-widget/Widget/StatusEntry.swift", "macos-widget/Widget/StatusView.swift", "macos-widget/Tests/Layout/main.swift", "-o", binary], options);
    execFileSync(binary, [output], options);
  } finally { rmSync(directory, { recursive: true, force: true }); }
} else { process.stdout.write("macOS widget layout fixtures require a macOS host.\n"); }
