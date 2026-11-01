import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import platforms from "../src/platforms.cjs";
import { packageRelease } from "./package.mjs";

// These deliberately non-native payloads exercise packaging only. Fixture npm
// manifests are private, and all release metadata retains fixture: true.
export function dryRun() {
  const directory = mkdtempSync(path.join(tmpdir(), "ccow-fixture-"));
  try {
    const binaries = Object.fromEntries(
      platforms.targets.map((target) => {
        const file = path.join(directory, target.binary + "-" + target.suffix);
        writeFileSync(
          file,
          "#!/bin/sh\n# CacheCow packaging fixture, not the native CLI.\nprintf 'fixture:%s\\n' \"$@\"\n",
          { mode: 0o755 },
        );
        return [target.suffix, file];
      }),
    );
    const result = packageRelease({
      binaries,
      output: path.join(directory, "release"),
      fixture: true,
    });
    return {
      ...result,
      validation: "fixture packaging only; no publication or native acceptance",
    };
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}
if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
)
  console.log(JSON.stringify(dryRun()));
