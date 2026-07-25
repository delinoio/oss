import { resolve } from "node:path";

import { run } from "./process.mjs";

const appRoot = resolve(import.meta.dirname, "..");
const androidRoot = resolve(appRoot, "src-tauri/gen/android");
const gradle =
  process.platform === "win32"
    ? resolve(androidRoot, "gradlew.bat")
    : resolve(androidRoot, "gradlew");

await run(gradle, ["testX86_64DebugUnitTest", "--no-daemon"], {
  cwd: androidRoot,
});
