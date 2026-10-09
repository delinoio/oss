import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { dryRunEnvironment } from "./bundle-macos-dry-run.mjs";

if (process.platform === "darwin") {
  const directory = mkdtempSync(join(tmpdir(), "delidev-widget-tests-"));
  try {
    const app = fileURLToPath(new URL("..", import.meta.url));
    const options = { cwd: app, env: dryRunEnvironment(process.env), stdio: "inherit" };
    const binary = join(directory, "widget-tests");
    execFileSync("xcrun", ["swiftc", "macos-widget/Shared/Snapshot.swift", "macos-widget/Shared/Localization.swift", "macos-widget/Shared/SnapshotStore.swift", "macos-widget/Tests/main.swift", "-o", binary], options);
    execFileSync(binary, [], options);
  } finally { rmSync(directory, { recursive: true, force: true }); }
} else { process.stdout.write("macOS widget native fixtures require a macOS host.\n"); }
