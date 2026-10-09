// SPDX-License-Identifier: Apache-2.0
export const defaultBranchPrefix = "delidev/";
export function validBranchPrefix(prefix: string): boolean {
 if (prefix === "") return true;
 // TextEncoder replaces invalid UTF-16. Reject it rather than silently changing input.
 if (/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(prefix) || new TextEncoder().encode(prefix).length > 256) return false;
 const ref = prefix + "branch";
 return !ref.startsWith("-") && !ref.startsWith("/") && !ref.endsWith(".") && !ref.includes("..") && !ref.includes("@{") && !ref.includes("//") && !/[\x00-\x20\x7f~^:?*\[\\]/u.test(ref) && ref.split("/").every(part => !part.startsWith(".") && !part.endsWith(".lock"));
}
