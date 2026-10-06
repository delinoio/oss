import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

// Both applications use the same pinned Tauri CEF runtime. Keep its AppImage
// helper source, binary and license pins under the existing DevHud pin owner.
const cefPins = JSON.parse(readFileSync(fileURLToPath(new URL("../apps/devhud/cef-pins.json", import.meta.url)), "utf8"));

function appImageSharunAsset(architecture, sharunPin = cefPins.appImage.sharun) {
  const asset = sharunPin.assets[architecture];
  if (!asset) {
    throw new Error(`unsupported Linux AppImage architecture ${architecture}`);
  }
  return {
    sha256: asset.sha256,
    url: `${sharunPin.repository}/releases/download/${sharunPin.version}/${asset.name}`,
  };
}

export async function prepareVerifiedAppImageSharun(
  architecture = process.arch,
  fetchAsset = globalThis.fetch,
  sharunPin = cefPins.appImage.sharun,
  { anylinuxPin = cefPins.appImage.anylinux, runCompiler = spawnSync, environment = process.env } = {},
) {
  const asset = appImageSharunAsset(architecture, sharunPin);
  const response = await fetchAsset(asset.url, { signal: AbortSignal.timeout(120_000) });
  if (!response.ok) {
    throw new Error(`AppImage launcher download failed with HTTP ${response.status}`);
  }
  const bytes = Buffer.from(await response.arrayBuffer());
  const observedSha256 = createHash("sha256").update(bytes).digest("hex");
  if (observedSha256 !== asset.sha256) {
    throw new Error(
      `AppImage launcher checksum mismatch: expected ${asset.sha256}, observed ${observedSha256}`,
    );
  }

  // The pinned upstream script's default anylinux.c URL returns HTTP 404.
  // Verify the source and license from its owning fork before compiling locally;
  // quick-sharun preserves an existing lib/anylinux.so and adds it to .preload.
  // Remove this staging when Tauri pins and verifies these helper inputs itself.
  const directory = mkdtempSync(join(tmpdir(), "tauri-verified-sharun-"));
  const launcher = join(directory, "sharun");
  try {
    writeFileSync(launcher, bytes, { mode: 0o755, flag: "wx" });
    const base = `${anylinuxPin.repository.replace("https://github.com/", "https://raw.githubusercontent.com/")}/${anylinuxPin.revision}`;
    const verifiedFiles = {};
    for (const [kind, pin] of Object.entries({ source: anylinuxPin.source, license: anylinuxPin.license })) {
      const result = await fetchAsset(`${base}/${pin.path}`, { signal: AbortSignal.timeout(120_000) });
      if (!result.ok) throw new Error(`AppImage anylinux ${kind} download failed with HTTP ${result.status}`);
      const content = Buffer.from(await result.arrayBuffer());
      const digest = createHash("sha256").update(content).digest("hex");
      if (digest !== pin.sha256) throw new Error(`AppImage anylinux ${kind} checksum mismatch: expected ${pin.sha256}, observed ${digest}`);
      const path = join(directory, kind === "source" ? "anylinux.c" : "LICENSE-anylinux");
      writeFileSync(path, content, { mode: kind === "license" ? 0o644 : 0o600, flag: "wx" });
      verifiedFiles[kind] = path;
    }
    const library = join(directory, "anylinux.so");
    const compiled = runCompiler("cc", ["-shared", "-fPIC", "-O2", verifiedFiles.source, "-o", library], {
      shell: false, encoding: "utf8", timeout: 120_000, env: environment,
    });
    if (compiled.error || compiled.status !== 0) {
      throw new Error(`AppImage anylinux compilation failed: ${compiled.error?.message ?? compiled.stderr ?? compiled.status}`);
    }
    return {
      config: { bundle: { linux: { appimage: { files: {
        sharun: launcher,
        "lib/anylinux.so": library,
        "share/licenses/anylinux/LICENSE": verifiedFiles.license,
      } } } } },
      close: () => rmSync(directory, { recursive: true, force: true }),
    };
  } catch (error) {
    rmSync(directory, { recursive: true, force: true });
    throw error;
  }
}

export function appImageEnvironment(environment) {
  // The pinned launcher lacks complete GLib auxv support in its runtime probe.
  // Disable that build-time probe; retain all packaging integrity checks.
  // Remove this override after Tauri pins a corrected launcher and probe.
  const result = { ...environment, STRACE_MODE: "0", ANYLINUX_LIB: "1" };
  delete result.SHARUN_LINK;
  delete result.SKIP_INTEGRITY_CHECKS;
  delete result.ANYLINUX_LIB_SOURCE;
  return result;
}
