import { createHash, randomUUID } from "node:crypto";
import { open, realpath, unlink } from "node:fs/promises";
import { constants, closeSync, fsyncSync, linkSync, openSync, renameSync, unlinkSync, realpathSync, statSync } from "node:fs";
import { basename, dirname, join, resolve } from "node:path";
import { ForgeError, abortable, checkSignal } from "./errors.js";
import { ErrorCode, type AssetSource } from "./types.js";

export interface SourceFingerprint { path: string; digest: string }
export const digest = (bytes: Uint8Array) => createHash("sha256").update(bytes).digest("hex");

const outputQueues = new Map<string, Promise<void>>();

export async function withOutputReservation<T>(output: string, signal: AbortSignal, work: (destination: string) => Promise<T>): Promise<T> {
  checkSignal(signal);
  let directory: string;
  let key: string;
  try {
    directory = realpathSync(dirname(resolve(output)));
    const info = statSync(directory, { bigint: true });
    if (!info.isDirectory()) throw new Error("Not a directory");
    key = `${info.dev}:${info.ino}`;
  } catch { throw new ForgeError(ErrorCode.Io, "Unable to reserve the output directory."); }
  // Reserve synchronously in invocation order, before React/native preparation.
  // Directory identity also covers symlink and case aliases for absent outputs
  // on case-insensitive volumes; unrelated directories still run independently.
  const previous = outputQueues.get(key) ?? Promise.resolve();
  let release!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  const current = previous.then(() => held);
  outputQueues.set(key, current);
  void current.then(() => { if (outputQueues.get(key) === current) outputQueues.delete(key); });
  try {
    await abortable(previous, signal);
    checkSignal(signal);
    return await work(join(directory, basename(output)));
  } finally {
    // A cancelled waiter releases only its own slot. Its successors still wait
    // for the earlier publisher through the chained current promise.
    release();
  }
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
    // No portable conditional rename protects a source from another process's
    // atomic save after fingerprinting. Require a separate output path instead.
    // Windows realpath does not guarantee canonical filename casing. Reject
    // case aliases conservatively, including a source removed after import.
    const samePath = (a: string | undefined, b: string) => process.platform === "win32" ? a?.toUpperCase() === b.toUpperCase() : a === b;
    if (options.source && (samePath(existing, options.source.path) || samePath(destination, options.source.path))) {
      throw new ForgeError(ErrorCode.UnsupportedEdit, "Imported sources cannot be overwritten. Export to a separate output path.");
    }
    if (existing && !options.overwrite) throw new ForgeError(ErrorCode.Conflict, "Output already exists. Enable overwrite explicitly to replace it.");
    temporary = join(directory, `.react-forge-${randomUUID()}.tmp`);
    const file = await open(temporary, "wx", 0o600);
    try { await file.writeFile(bytes); await file.sync(); } finally { await file.close(); }
    // No await occurs between the final cancellation check and atomic
    // publication. A later abort must report success for already-published bytes.
    checkSignal(options.signal);
    if (options.overwrite) { renameSync(temporary, destination); published = true; }
    else { linkSync(temporary, destination); published = true; unlinkSync(temporary); }
    temporary = undefined;
    // Node cannot open/fsync directories on Windows. The temporary file was
    // flushed before its atomic rename/link, but directory crash durability is
    // guaranteed only on Unix where the directory synchronization is available.
    if (process.platform !== "win32") {
      const parent = openSync(directory, "r");
      try { fsyncSync(parent); } finally { closeSync(parent); }
    }
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
