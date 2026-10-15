// Export usage fixture: `leaf` is the provider that the other modules in this
// directory reach through different import shapes. Direct eval makes Rspack
// report a concatenation bailout, which gives the module a diagnostic.
import { back } from "./cycle-back.js";

export function leaf(code) {
  return eval(code) ?? back();
}
