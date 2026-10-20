// SPDX-License-Identifier: Apache-2.0
// This sink validates reference behavior; it is never linked into the Blitz executable.
let current = -1;
let initialized = false;
function nativeInit(html) {
  if (initialized || !html.includes('id="n1">0</p>'))
    throw new Error("initial HTML failed");
  initialized = true;
}
function nativeText(id, text) {
  if (id === 1) current = Number(text);
}
function nativeSnapshot(frame) {
  if (!initialized || current !== frame)
    throw new Error("text mutation failed");
  return 1;
}
