// SPDX-License-Identifier: Apache-2.0
const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const platforms = Object.freeze({
  "linux-x64": "linux-amd64",
  "linux-arm64": "linux-arm64",
  "darwin-x64": "darwin-amd64",
  "darwin-arm64": "darwin-arm64",
  "win32-x64": "windows-amd64",
});
function artifact(platform, arch) {
  const target = platforms[`${platform}-${arch}`];
  if (!target)
    throw new Error(`Unsupported TaiRouter platform: ${platform}/${arch}`);
  return `tairouter-${target}${platform === "win32" ? ".exe" : ""}`;
}
function verify(bytes, expected) {
  if (!/^[a-f0-9]{64}$/.test(expected ?? ""))
    throw new Error("Missing release checksum");
  if (crypto.createHash("sha256").update(bytes).digest("hex") !== expected)
    throw new Error("Release checksum mismatch");
}
async function install() {
  const name = artifact(process.platform, process.arch);
  const { version } = require("./package.json");
  if (!/^\d+\.\d+\.\d+(?:-[a-zA-Z0-9.-]+)?$/.test(version))
    throw new Error("Invalid release version");
  // Checksums are embedded in the npm tarball, not fetched beside the executable.
  const checksums = require("./checksums.json");
  if (!/^[a-f0-9]{64}$/.test(checksums[name] ?? ""))
    throw new Error("Missing release checksum");
  const response = await fetch(
    `https://github.com/delinoio/oss/releases/download/tairouter@v${version}/${name}`,
    { signal: AbortSignal.timeout(120000) },
  );
  if (!response.ok || !response.url.startsWith("https://"))
    throw new Error("Cannot download release executable");
  const chunks = [];
  let size = 0;
  for await (const chunk of response.body) {
    size += chunk.length;
    if (size > 128 * 1024 * 1024)
      throw new Error("Release executable exceeds size limit");
    chunks.push(chunk);
  }
  const bytes = Buffer.concat(chunks);
  verify(bytes, checksums[name]);
  const directory = path.join(__dirname, "native");
  fs.mkdirSync(directory, { recursive: true });
  const destination = path.join(
    directory,
    process.platform === "win32" ? "tairouter.exe" : "tairouter",
  );
  const temporary = `${destination}.${crypto.randomUUID()}.tmp`;
  try {
    fs.writeFileSync(temporary, bytes, { mode: 0o755, flag: "wx" });
    fs.renameSync(temporary, destination);
  } finally {
    fs.rmSync(temporary, { force: true });
  }
}
module.exports = { artifact, verify };
if (require.main === module)
  install().catch(() => {
    console.error(
      "TaiRouter installation failed. Verify the release, network access, and package checksums.",
    );
    process.exitCode = 1;
  });
