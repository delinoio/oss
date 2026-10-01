import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { targets } = require("../src/platforms.cjs");
const runners = Object.freeze({
  "darwin-x64": "macos-15-intel",
  "darwin-arm64": "macos-14",
  "linux-x64-gnu": "ubuntu-22.04",
  "linux-arm64-gnu": "ubuntu-22.04-arm",
});

// The package registry owns the release set. Adding a platform also requires
// explicitly choosing a native host; cross-compilation cannot fill that slot.
export const nativeMatrix = Object.freeze({ include: targets.map(({ suffix, rust }) => {
  const runner = runners[suffix];
  if (!runner) throw new Error(`Missing pnport native runner: ${suffix}`);
  return { runner, target: rust, suffix };
}) });
