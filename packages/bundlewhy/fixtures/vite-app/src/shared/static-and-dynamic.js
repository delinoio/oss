// Blocker for Vite: statically and dynamically imported, so the dynamic import
// cannot move it into a separate chunk.
export function shared() {
  return "shared";
}
