// Blocker: direct eval keeps every name in reach of this scope unmangled.
export function evaluate(expression) {
  const localNameThatCannotBeMangled = 40;
  return eval(expression) + localNameThatCannotBeMangled;
}
