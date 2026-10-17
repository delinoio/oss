import assert from "node:assert/strict";
import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  Code,
  resolveExecutable,
  resolveWindowsScriptcShim,
  versions,
} from "../dist/core.js";

for (const layout of ["global", "local"]) {
  test(`resolve ${layout} npm Windows shim to the pinned native compiler`, async () => {
    const root = await mkdtemp(join(tmpdir(), "typenative shim & "));
    try {
      const bin =
        layout === "global" ? root : join(root, "node_modules", ".bin");
      const pkg = join(root, "node_modules", "scriptc");
      await mkdir(bin, { recursive: true });
      await mkdir(join(pkg, "bin"), { recursive: true });
      const executable = join(pkg, "bin", "scriptc.exe"),
        shim = join(bin, "scriptc.cmd");
      await writeFile(shim, "@echo off\r\n", { mode: 0o755 });
      await writeFile(join(bin, "scriptc"), "#!/bin/sh\nexit 1\n", {
        mode: 0o755,
      });
      await writeFile(executable, "fixture header");
      await writeFile(
        join(pkg, "package.json"),
        JSON.stringify({
          name: "scriptc",
          version: versions.scriptc,
          bin: { scriptc: "bin/scriptc.exe" },
        }),
      );
      assert.equal(resolveExecutable("scriptc", "win32", bin), executable);
      assert.equal(resolveExecutable(shim, "win32"), executable);
      await writeFile(
        join(pkg, "package.json"),
        JSON.stringify({
          name: "scriptc",
          version: "wrong",
          bin: { scriptc: "../other.exe" },
        }),
      );
      assert.throws(() => resolveWindowsScriptcShim(shim), { code: Code.Tool });
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  });
}
