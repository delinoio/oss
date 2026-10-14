// Counterexample: declared side-effect free and unused, so it is removed.
globalThis.__bundlewhyPure = true;

export function unusedPure() {
  return "unused pure export";
}
