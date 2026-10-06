import { stat } from "node:fs/promises";
import { isAbsolute } from "node:path";

// CI builds this executable once in its own runner. Only immutable executable
// bytes are shared; every fixture still owns its processes and private state.
export async function fixtureBinary(fallback, env = process.env) {
  const binary = env.DELIDEV_TEST_BINARY;
  if (binary === undefined) return fallback;
  if (!isAbsolute(binary) || !(await stat(binary)).isFile()) throw new Error("Invalid DeliDev fixture executable");
  return binary;
}
