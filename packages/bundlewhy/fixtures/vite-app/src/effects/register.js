// Blocker: no export is used, but the top-level call is a side effect.
globalThis.__bundlewhyRegistry = (globalThis.__bundlewhyRegistry || 0) + 1;

export function unusedEffectful() {
  return "unused effectful export";
}
