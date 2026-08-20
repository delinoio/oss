#!/usr/bin/env node

import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const root = JSON.parse(readFileSync(join(appRoot, "updater-trust-root.json"), "utf8"));
const updaterRust = readFileSync(join(appRoot, "src-tauri/src/updater.rs"), "utf8");
const publicKey = Buffer.from(root.publicKey, "base64");
const fingerprint = createHash("sha256").update(publicKey).digest("hex");
const placeholderFingerprint = "21fe31dfa154a261626bf854046fd2271b7bed4b6abe45aa58877ef47f9721b9";

if (root.schemaVersion !== 1 || root.keyId !== "devhud-release-root-v1" || root.algorithm !== "ed25519") throw new Error("unsupported DevHud updater trust root");
if (publicKey.length !== 32 || root.fingerprint !== fingerprint) throw new Error("DevHud updater trust root fingerprint mismatch");
if (root.productionReady !== true || root.fingerprint === placeholderFingerprint) throw new Error("DevHud updater release is blocked: replace the explicit placeholder public key and set productionReady only after the key ceremony");
if (!updaterRust.includes(root.publicKey) || !updaterRust.includes(root.fingerprint) || !updaterRust.includes("ROOT_PRODUCTION_READY: bool = true")) throw new Error("DevHud native updater trust root or readiness gate does not match release metadata");

console.log(`devhud: updater release root ${root.keyId} (${root.fingerprint}) is production-ready`);
