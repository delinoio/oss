// Counterexample: no dynamic scope access, so local names can be mangled.
export function plainAdd(left, right) {
  const localNameThatCanBeMangled = left + right;
  return localNameThatCanBeMangled;
}
