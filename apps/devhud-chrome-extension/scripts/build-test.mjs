import { spawnSync } from "node:child_process";

const packageManagerPath = process.env.npm_execpath;
if (!packageManagerPath) throw new Error("package manager entrypoint is unavailable");

const result = spawnSync(process.execPath, [packageManagerPath, "run", "build"], {
  env: { ...process.env, DEVHUD_EXTENSION_TEST_BUILD: "1" },
  stdio: "inherit",
});
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
