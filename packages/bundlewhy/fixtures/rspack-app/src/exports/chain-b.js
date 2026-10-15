import { leaf } from "./leaf.js";

export function chainB(code) {
  return leaf(`(${code})`);
}
