import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const { targets } = require("../src/platforms.cjs");

function identity(version, revision) {
  if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version)) throw new Error("Invalid exact stable version");
  if (!/^[0-9a-f]{40}$/.test(revision)) throw new Error("A complete source revision is required");
  return { version, gitHead: revision, license: "Apache-2.0", engines: { node: ">=22" }, repository: { type: "git", url: "git+https://github.com/delinoio/oss.git" } };
}

export function launcherManifest(version, revision) {
  return {
    ...identity(version, revision), name: "@delino/pnport",
    description: "Run subprocesses through an installed Yarn 4 Plug'n'Play filesystem",
    bin: { pnport: "bin/pnport.cjs" },
    files: ["bin", "src", "README.md", "LICENSE"],
    optionalDependencies: Object.fromEntries(targets.map(target => [target.name, version])),
  };
}

export function nativeManifest(suffix, version, revision) {
  const target = targets.find(target => target.suffix === suffix);
  if (!target) throw new Error("Unsupported pnport target");
  return {
    ...identity(version, revision), name: target.name,
    os: [target.os], cpu: [target.cpu], ...(target.libc ? { libc: [target.libc] } : {}),
    preferUnplugged: true,
    files: ["bin", "README.md", "LICENSE"],
  };
}

export function companion(target) {
  return target.os === "darwin" ? "libpnport_preload.dylib" : target.os === "win32" ? "pnport_preload.dll" : "libpnport_preload.so";
}
