import { createHash, randomUUID } from "node:crypto";
import { open, realpath, unlink } from "node:fs/promises";
import { constants, closeSync, fsyncSync, fstatSync, linkSync, openSync, readSync, renameSync, unlinkSync } from "node:fs";
import { basename, dirname, join, resolve } from "node:path";
import { ForgeError, checkSignal } from "./errors.js";
import { ErrorCode, type AssetSource } from "./types.js";

export interface SourceFingerprint { path: string; digest: string }
export const digest = (bytes: Uint8Array) => createHash("sha256").update(bytes).digest("hex");

function sourceDigest(path: string, limit: number): string {
  const fd = openSync(path, constants.O_RDONLY | constants.O_NONBLOCK);
  try {
    const stat = fstatSync(fd);
    if (!stat.isFile() || stat.size > limit) throw new ForgeError(ErrorCode.Conflict, "Imported source changed externally.");
    const hash = createHash("sha256");
    const chunk = Buffer.allocUnsafe(64 * 1024);
    let total = 0;
    for (;;) {
      const size = readSync(fd, chunk);
      if (!size) break;
      total += size;
      if (total > limit) throw new ForgeError(ErrorCode.Conflict, "Imported source changed externally.");
      hash.update(chunk.subarray(0, size));
    }
    const after = fstatSync(fd);
    if (after.size !== stat.size || after.ctimeMs !== stat.ctimeMs || after.mtimeMs !== stat.mtimeMs) {
      throw new ForgeError(ErrorCode.Conflict, "Imported source changed externally.");
    }
    return hash.digest("hex");
  } finally { closeSync(fd); }
}

export async function readSource(source: AssetSource, limit: number, signal?: AbortSignal): Promise<{ bytes: Buffer; fingerprint?: SourceFingerprint }> {
  checkSignal(signal);
  if (source instanceof Uint8Array) {
    if (source.byteLength > limit) throw new ForgeError(ErrorCode.ResourceLimit, "Input exceeds its byte limit.");
    return { bytes: Buffer.from(source) };
  }
  let handle;
  try {
    const path = await realpath(source.path);
    handle = await open(path, constants.O_RDONLY | constants.O_NONBLOCK);
    const before = await handle.stat();
    if (!before.isFile()) throw new ForgeError(ErrorCode.MalformedInput, "Input must be a regular file.");
    if (before.size > limit) throw new ForgeError(ErrorCode.ResourceLimit, "Input exceeds its byte limit.");
    const chunks: Buffer[] = [];
    let total = 0;
    for (;;) {
      checkSignal(signal);
      const chunk = Buffer.allocUnsafe(Math.min(64 * 1024, limit - total + 1));
      const { bytesRead } = await handle.read(chunk);
      if (!bytesRead) break;
      total += bytesRead;
      if (total > limit) throw new ForgeError(ErrorCode.ResourceLimit, "Input exceeds its byte limit.");
      chunks.push(chunk.subarray(0, bytesRead));
    }
    const after = await handle.stat();
    if (after.size !== before.size || after.mtimeMs !== before.mtimeMs || after.ctimeMs !== before.ctimeMs) {
      throw new ForgeError(ErrorCode.Conflict, "Input changed while it was being read. Retry the import.");
    }
    const bytes = Buffer.concat(chunks, total);
    return { bytes, fingerprint: { path, digest: digest(bytes) } };
  } catch (error) {
    if (error instanceof ForgeError) throw error;
    throw new ForgeError(ErrorCode.Io, "Unable to read the explicit input file.");
  } finally { await handle?.close(); }
}

export async function publish(bytes: Buffer, output: string, options: {
  overwrite?: boolean; signal?: AbortSignal; source?: SourceFingerprint;
}): Promise<{ published: true }> {
  checkSignal(options.signal);
  let temporary: string | undefined;
  let published = false;
  try {
    const absolute = resolve(output);
    const directory = await realpath(dirname(absolute));
    const destination = join(directory, basename(absolute));
    const existing = await realpath(destination).catch((error: NodeJS.ErrnoException) => {
      if (error.code === "ENOENT") return undefined;
      throw error;
    });
    if (existing && !options.overwrite) throw new ForgeError(ErrorCode.Conflict, "Output already exists. Enable overwrite explicitly to replace it.");
    temporary = join(directory, `.react-forge-${randomUUID()}.tmp`);
    const file = await open(temporary, "wx", 0o600);
    try { await file.writeFile(bytes); await file.sync(); } finally { await file.close(); }
    // No await occurs between the final cancellation/source check and atomic
    // publication. A later abort must report success for already-published bytes.
    checkSignal(options.signal);
    if (options.source && (existing === options.source.path || destination === options.source.path)) {
      if (sourceDigest(options.source.path, 256 * 1024 * 1024) !== options.source.digest) {
        throw new ForgeError(ErrorCode.Conflict, "Imported source changed externally. Import the new source before replacing it.");
      }
    }
    if (options.overwrite) { renameSync(temporary, destination); published = true; }
    else { linkSync(temporary, destination); published = true; unlinkSync(temporary); }
    temporary = undefined;
    if (options.source && destination === options.source.path) options.source.digest = digest(bytes);
    const parent = openSync(directory, "r");
    try { fsyncSync(parent); } finally { closeSync(parent); }
    return { published: true };
  } catch (error) {
    if (published) {
      // Publication cannot be rolled back safely after another process can see
      // the new file. Report the completed write even if durability sync failed.
      throw new ForgeError(ErrorCode.Io, "Output was published, but directory durability could not be confirmed.", { published: true });
    }
    if (error instanceof ForgeError) throw error;
    if ((error as NodeJS.ErrnoException).code === "EEXIST") throw new ForgeError(ErrorCode.Conflict, "Output already exists.");
    throw new ForgeError(ErrorCode.Io, "File publication failed; existing output was preserved.");
  } finally { if (temporary) await unlink(temporary).catch(() => {}); }
}
