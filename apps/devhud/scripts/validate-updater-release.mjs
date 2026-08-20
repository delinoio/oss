#!/usr/bin/env node

import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const placeholderFingerprint = "21fe31dfa154a261626bf854046fd2271b7bed4b6abe45aa58877ef47f9721b9";

function withoutRustComments(source) {
  let output = "";
  const codePositions = [];
  let index = 0;
  let blockDepth = 0;
  let state = "code";
  let rawTerminator = "";
  const append = (value, isCode) => {
    output += value;
    for (let offset = 0; offset < value.length; offset += 1) codePositions.push(isCode);
  };
  while (index < source.length) {
    const current = source[index];
    const next = source[index + 1];
    if (state === "line-comment") {
      if (current === "\n") { state = "code"; append(current, true); } else append(" ", false);
      index += 1;
      continue;
    }
    if (state === "block-comment") {
      if (current === "/" && next === "*") { blockDepth += 1; append("  ", false); index += 2; continue; }
      if (current === "*" && next === "/") {
        blockDepth -= 1;
        append("  ", false);
        index += 2;
        if (blockDepth === 0) state = "code";
        continue;
      }
      append(current === "\n" ? "\n" : " ", false);
      index += 1;
      continue;
    }
    if (state === "raw-string") {
      if (source.startsWith(rawTerminator, index)) {
        append(rawTerminator, false);
        index += rawTerminator.length;
        state = "code";
      } else {
        append(current, false);
        index += 1;
      }
      continue;
    }
    if (state === "string" || state === "character") {
      append(current, false);
      if (current === "\\" && next !== undefined) { append(next, false); index += 2; continue; }
      if ((state === "string" && current === "\"") || (state === "character" && current === "'")) state = "code";
      index += 1;
      continue;
    }
    if (current === "/" && next === "/") { state = "line-comment"; append("  ", false); index += 2; continue; }
    if (current === "/" && next === "*") { state = "block-comment"; blockDepth = 1; append("  ", false); index += 2; continue; }
    const rawStart = source.slice(index).match(/^(?:b|c)?r(#{0,255})"/u);
    if (rawStart) {
      state = "raw-string";
      rawTerminator = `"${rawStart[1]}`;
      append(rawStart[0], false);
      index += rawStart[0].length;
      continue;
    }
    if (current === "\"") state = "string";
    else if (current === "'" && /^'(?:\\.|[^'\\])'/u.test(source.slice(index))) state = "character";
    append(current, state === "code");
    index += 1;
  }
  if (state === "block-comment" || state === "raw-string") throw new Error("unterminated Rust comment or raw string in updater source");
  return { source: output, codePositions };
}

function exactConstant(active, name, type, valuePattern) {
  const expression = new RegExp(`^([\\t ]*)pub const ${name}: ${type} =[\\t \\r\\n]*${valuePattern};`, "gmu");
  const matches = [...active.source.matchAll(expression)].filter((match) => active.codePositions[match.index + match[1].length]);
  if (matches.length !== 1) throw new Error(`expected exactly one active native ${name} declaration`);
  return matches[0][2];
}

export function parseNativeTrustRoot(source) {
  const active = withoutRustComments(source);
  return {
    keyId: exactConstant(active, "ROOT_KEY_ID", "&str", '"([^"\\r\\n]*)"'),
    publicKey: exactConstant(active, "ROOT_PUBLIC_KEY_BASE64", "&str", '"([^"\\r\\n]*)"'),
    fingerprint: exactConstant(active, "ROOT_FINGERPRINT", "&str", '"([^"\\r\\n]*)"'),
    productionReady: exactConstant(active, "ROOT_PRODUCTION_READY", "bool", "(true|false)") === "true",
  };
}

export function validateUpdaterRelease(root, updaterRust) {
  const publicKey = Buffer.from(root.publicKey, "base64");
  const fingerprint = createHash("sha256").update(publicKey).digest("hex");
  if (root.schemaVersion !== 1 || root.keyId !== "devhud-release-root-v1" || root.algorithm !== "ed25519") throw new Error("unsupported DevHud updater trust root");
  if (publicKey.length !== 32 || root.fingerprint !== fingerprint) throw new Error("DevHud updater trust root fingerprint mismatch");
  if (root.productionReady !== true || root.fingerprint === placeholderFingerprint) throw new Error("DevHud updater release is blocked: replace the explicit placeholder public key and set productionReady only after the key ceremony");
  const native = parseNativeTrustRoot(updaterRust);
  if (native.keyId !== root.keyId || native.publicKey !== root.publicKey || native.fingerprint !== root.fingerprint || native.productionReady !== root.productionReady) throw new Error("DevHud native updater trust root or readiness gate does not match release metadata");
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const root = JSON.parse(readFileSync(join(appRoot, "updater-trust-root.json"), "utf8"));
  const updaterRust = readFileSync(join(appRoot, "src-tauri/src/updater.rs"), "utf8");
  validateUpdaterRelease(root, updaterRust);
  console.log(`devhud: updater release root ${root.keyId} (${root.fingerprint}) is production-ready`);
}
