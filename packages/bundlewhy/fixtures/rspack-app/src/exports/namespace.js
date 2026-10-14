import * as leafModule from "./leaf.js";

export function viaNamespace(name, code) {
  return leafModule[name](code);
}
