import { hostTarget } from "../dist/core.js";
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, readFile, rm, writeFile, access } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";

const hasCCompiler =
  process.platform !== "win32" &&
  spawnSync("cc", ["--version"], { stdio: "ignore" }).status === 0;

for (const phase of ["compile", "execute"]) {
  for (const signal of ["SIGINT", "SIGTERM", "SIGKILL"]) {
    test(
      `React probe cleans up after ${signal} during ${phase}`,
      {
        skip: !hostTarget()
          ? "Native probe does not support this host target"
          : process.platform === "win32"
            ? "POSIX signal fixture requires a Unix host"
            : phase === "execute" && !hasCCompiler
              ? "Native execution fixture requires cc; compilation interruption remains covered"
              : false,
        timeout: 15000,
      },
      async () => {
        const root = await mkdtemp(
          join(tmpdir(), "typenative-interruption-test-"),
        );
        let child;
        try {
          const marker = join(root, "ready");
          const executable = join(root, "fixture");
          const source = join(root, "fixture.c");
          // This native fixture tests process cleanup only, not React acceptance.
          await writeFile(
            source,
            '#include <stdio.h>\n#include <stdlib.h>\n#include <unistd.h>\nint main(void) { FILE *f = fopen(getenv("TN_MARKER"), "w"); fprintf(f, "%d", getpid()); fclose(f); sleep(30); return 0; }\n',
          );
          if (phase === "execute") {
            const built = spawnSync("cc", [source, "-o", executable], {
              encoding: "utf8",
            });
            assert.equal(built.status, 0, built.stderr);
          }
          const compiler = join(root, "scriptc");
          await writeFile(
            compiler,
            `#!/bin/sh
if [ "$1" = "--version" ]; then echo 0.3.5; exit 0; fi
for artifact do :; done
printf '%s' "$artifact" > "$TN_ARTIFACT"
if [ "$TN_PHASE" = "compile" ]; then
  printf '%s' "$$" > "$TN_MARKER"
  exec sleep 30
fi
cp "$TN_EXECUTABLE" "$artifact"
printf '%s' '{"schema":"scriptc-diagnostics","schemaVersion":1,"compilerVersion":"0.3.5","success":true,"diagnostics":[]}'
`,
            { mode: 0o755 },
          );
          child = spawn(
            process.execPath,
            [fileURLToPath(new URL("native-react.mjs", import.meta.url))],
            {
              env: {
                ...process.env,
                TN_SCRIPTC: compiler,
                TN_PHASE: phase,
                TN_MARKER: marker,
                TN_ARTIFACT: join(root, "artifact"),
                TN_EXECUTABLE: executable,
              },
              stdio: ["ignore", "pipe", "pipe"],
            },
          );
          let stdout = "",
            stderr = "";
          child.stdout.on("data", (chunk) => {
            stdout += chunk;
          });
          child.stderr.on("data", (chunk) => {
            stderr += chunk;
          });
          const closed = once(child, "close");
          let pid;
          for (let attempt = 0; attempt < 200; attempt++) {
            try {
              pid = Number(await readFile(marker, "utf8"));
              if (pid) break;
            } catch {}
            await delay(25);
          }
          assert.ok(pid, "child reached the selected phase");
          const artifact = await readFile(join(root, "artifact"), "utf8");
          assert.equal(child.kill(signal), true);
          const [code, killedBy] = await closed;
          assert.equal(code, signal === "SIGKILL" ? null : 1);
          assert.equal(killedBy, signal === "SIGKILL" ? signal : null);
          assert.equal(stderr, "");
          const result = JSON.parse(stdout);
          assert.equal(result.code, "TN_PROBE_INTERRUPTED");
          assert.equal(result.phase, phase);
          assert.equal(result.status, "failed");
          await assert.rejects(access(join(artifact, "..")), {
            code: "ENOENT",
          });
          assert.throws(() => process.kill(pid, 0), { code: "ESRCH" });
        } finally {
          if (child && child.exitCode === null) child.kill("SIGKILL");
          await rm(root, { recursive: true, force: true });
        }
      },
    );
  }
}
