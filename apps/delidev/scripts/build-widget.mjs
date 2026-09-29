import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";
import { dryRunEnvironment } from "./bundle-macos-dry-run.mjs";

// Extensions use the same native host architecture and credential-free local
// signing boundary as the app. Production signing/publication is separate.
if (process.platform === "darwin") {
  const app = fileURLToPath(new URL("..", import.meta.url));
  const arch = process.arch === "arm64" ? "arm64" : "x86_64";
  const options = { cwd: app, env: dryRunEnvironment(process.env), stdio: "inherit" };
  const output = resolve(app, "../../target/delidev-widget");
  // App Groups require a provisioned identity in Xcode's signing phase. Local
  // dry runs intentionally have none: compile first, then ad-hoc sign with the
  // declared entitlements. This verifies structure, not provisioned group access.
  execFileSync("xcodebuild", ["-project", "macos-widget/DeliDevWidget.xcodeproj", "-alltargets", "-configuration", "Release", "build", `ARCHS=${arch}`, "ONLY_ACTIVE_ARCH=YES", "CODE_SIGNING_ALLOWED=NO", "DEVELOPMENT_TEAM=", `SYMROOT=${output}`], options);
  for (const [name, folder] of [["DeliDevWidget", "Widget"], ["DeliDevWidgetSelection", "Intent"]]) {
    execFileSync("codesign", ["--force", "--sign", "-", "--entitlements", `macos-widget/${folder}/Widget.entitlements`, "--generate-entitlement-der", `${output}/Release/${name}.appex`], options);
  }
}
