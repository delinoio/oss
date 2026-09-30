import { createHash } from "node:crypto";
import { lstatSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { crc32 } from "node:zlib";
import { exitLikeChild, spawnDevServer } from "../../../scripts/spawn-dev-server.mjs";

export const desktopIcon = "apps/delidev/src-tauri/icons/icon-source@2x.png";
const repository = fileURLToPath(new URL("../../..", import.meta.url));
const success = { code: 0, signal: null };
const signature = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
const recovery = `From the repository root, run git lfs fetch --include=${desktopIcon} --exclude= and git lfs checkout ${desktopIcon}, then retry.`;
const Failure = Object.freeze({
  Asset: "invalid-icon",
  Git: "git-unavailable",
  Lfs: "git-lfs-unavailable",
  Checkout: "icon-checkout-failed",
  Download: "icon-download-failed",
  Integrity: "icon-integrity-failed",
  Modified: "icon-locally-modified",
});
const guidance = {
  [Failure.Asset]: "The DeliDev source icon is missing, unreadable, or not a valid PNG/LFS pointer. Preserve local edits and restore a valid icon before retrying.",
  [Failure.Git]: `Git could not be started. Install Git and Git LFS. ${recovery}`,
  [Failure.Lfs]: `Git LFS is unavailable. Install Git LFS. ${recovery}`,
  [Failure.Checkout]: `Git LFS could not restore the icon. Use a complete writable Git checkout. ${recovery}`,
  [Failure.Download]: `Git LFS could not download the icon. Check the repository remote, network access, and Git authentication. ${recovery}`,
  [Failure.Integrity]: `The icon was not restored to its original LFS size, SHA-256, and PNG format. Preserve any local edits and check the LFS object. ${recovery}`,
  [Failure.Modified]: "The icon cannot be confirmed as an unchanged committed LFS pointer. Preserve local edits and check the Git checkout before retrying.",
};

class AssetFailure extends Error {
  constructor(code, result = { code: 1, signal: null }) {
    super(code);
    this.code = code;
    this.result = result;
  }
}

// Check PNG container integrity before invoking expensive native tools. Tauri
// remains the pixel decoder; restored LFS bytes must also match the original
// pointer's complete digest, so a truncated or substituted object cannot pass.
function isPng(bytes) {
  if (!bytes.subarray(0, 8).equals(signature)) return false;
  let imageData = false;
  for (let offset = 8; offset + 12 <= bytes.length;) {
    const length = bytes.readUInt32BE(offset);
    const end = offset + 8 + length;
    if (end + 4 > bytes.length) return false;
    const type = bytes.toString("ascii", offset + 4, offset + 8);
    if (crc32(bytes.subarray(offset + 4, end)) !== bytes.readUInt32BE(end)) return false;
    if (offset === 8) {
      if (type !== "IHDR" || length !== 13 || !bytes.readUInt32BE(offset + 8) || !bytes.readUInt32BE(offset + 12)) return false;
    } else if (type === "IHDR") return false;
    if (type === "IDAT") imageData = true;
    if (type === "IEND") return imageData && length === 0 && end + 4 === bytes.length;
    offset = end + 4;
  }
  return false;
}

export async function prepareAssets({
  root = repository,
  environment = process.env,
  run = spawnDevServer,
  log = entry => process.stderr.write(`${JSON.stringify(entry)}\n`),
} = {}) {
  let stage = "inspect";
  const report = (state, fields = {}) => log({ operation: "desktop_assets", asset: desktopIcon, stage, state, ...fields });
  const path = resolve(root, desktopIcon);
  const read = () => {
    // Git must never replace a user-selected symlink or another file kind.
    if (!lstatSync(path).isFile()) throw new AssetFailure(Failure.Asset);
    return readFileSync(path);
  };
  const git = async (args, failure) => {
    let result;
    try {
      // Git diagnostics may contain private remotes or credential-helper output.
      // Report only the stable stage, exit status, and actionable guidance below.
      result = await run("git", args, { cwd: root, env: environment, stdio: "ignore", shell: false }, { terminateProcessTree: true });
    } catch (error) {
      throw new AssetFailure(error.code === "ENOENT" ? Failure.Git : failure);
    }
    if (result.code !== 0 || result.signal !== null) throw new AssetFailure(failure, result);
  };
  try {
    const original = read();
    if (isPng(original)) {
      report("ready");
      return success;
    }
    const pointer = /^version https:\/\/git-lfs\.github\.com\/spec\/v1\noid sha256:([0-9a-f]{64})\nsize ([1-9][0-9]*)\n$/.exec(original.toString("utf8"));
    if (!pointer) throw new AssetFailure(Failure.Asset);
    const restored = () => {
      const bytes = read();
      if (bytes.equals(original)) return false;
      if (BigInt(bytes.length) !== BigInt(pointer[2]) || createHash("sha256").update(bytes).digest("hex") !== pointer[1] || !isPng(bytes)) {
        throw new AssetFailure(Failure.Integrity);
      }
      return true;
    };
    stage = "local-cache";
    report("started");
    await git(["lfs", "version"], Failure.Lfs);
    const checkout = async () => {
      // LFS compares pointer object IDs. Also check the committed bytes so an
      // edited pointer with the same object ID is not silently replaced.
      await git(["diff", "--quiet", "--no-ext-diff", "--no-textconv", "HEAD", "--", desktopIcon], Failure.Modified);
      await git(["lfs", "checkout", desktopIcon], Failure.Checkout);
    };
    // checkout restores only unchanged pointers and never overwrites local edits.
    await checkout();
    if (!restored()) {
      stage = "download";
      report("started");
      // Override ambient include/exclude and recent-ref settings: only this
      // checkout's exact icon is needed, not other large repository assets.
      await git(["-c", "lfs.fetchrecentalways=false", "lfs", "fetch", `--include=${desktopIcon}`, "--exclude="], Failure.Download);
      if (!restored()) {
        await checkout();
        if (!restored()) throw new AssetFailure(Failure.Integrity);
      }
    }
    report("ready");
    return success;
  } catch (error) {
    const failure = error instanceof AssetFailure ? error : new AssetFailure(Failure.Asset);
    report(failure.result.signal ? "interrupted" : "failed", { code: failure.code, exitCode: failure.result.code, signal: failure.result.signal, guidance: guidance[failure.code] });
    return failure.result;
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  exitLikeChild(await prepareAssets());
}
