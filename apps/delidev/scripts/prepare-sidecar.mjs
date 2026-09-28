import { execFileSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";

const app = fileURLToPath(new URL("..", import.meta.url));
const root = resolve(app, "../..");
const targets = new Map([
  ["aarch64-apple-darwin", ["darwin", "arm64"]],
  ["x86_64-apple-darwin", ["darwin", "amd64"]],
  ["x86_64-pc-windows-msvc", ["windows", "amd64"]],
  ["aarch64-pc-windows-msvc", ["windows", "arm64"]],
  ["x86_64-unknown-linux-gnu", ["linux", "amd64"]],
  ["aarch64-unknown-linux-gnu", ["linux", "arm64"]],
]);
const args = process.argv.slice(2);
if (args.length > 1) throw new Error("Pass at most one explicit Rust target triple.");
const target = args[0] ?? execFileSync("rustc", ["--print", "host-tuple"], { cwd: root, encoding: "utf8" }).trim();
const platform = targets.get(target);
if (!platform) throw new Error("Unsupported DeliDev desktop target.");
const [goos, goarch] = platform;
const directory = resolve(app, "src-tauri/binaries");
mkdirSync(directory, { recursive: true });
const output = resolve(directory, `delidev-${target}${goos === "windows" ? ".exe" : ""}`);
execFileSync("go", ["build", "-trimpath", "-o", output, "./cmds/delidev-cli"], {
  cwd: root, stdio: "inherit", env: { ...process.env, GOOS: goos, GOARCH: goarch, CGO_ENABLED: "0" },
});
