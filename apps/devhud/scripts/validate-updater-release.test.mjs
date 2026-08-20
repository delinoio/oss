import assert from "node:assert/strict";
import test from "node:test";

import { parseNativeTrustRoot } from "./validate-updater-release.mjs";

const declarations = `
pub const ROOT_KEY_ID: &str = "devhud-release-root-v1";
pub const ROOT_PUBLIC_KEY_BASE64: &str = "native-key";
pub const ROOT_FINGERPRINT: &str =
    "native-fingerprint";
pub const ROOT_PRODUCTION_READY: bool = true;
`;

test("parses the exact active native trust declarations", () => {
  assert.deepEqual(parseNativeTrustRoot(declarations), {
    keyId: "devhud-release-root-v1",
    publicKey: "native-key",
    fingerprint: "native-fingerprint",
    productionReady: true,
  });
});

test("ignores matching trust values and readiness declarations in comments", () => {
  const stale = declarations
    .replace('"native-key"', '"stale-key"')
    .replace("ROOT_PRODUCTION_READY: bool = true", "ROOT_PRODUCTION_READY: bool = false");
  const parsed = parseNativeTrustRoot(`${stale}\n// pub const ROOT_PRODUCTION_READY: bool = true;\n/* native-key native-fingerprint */`);
  assert.equal(parsed.publicKey, "stale-key");
  assert.equal(parsed.productionReady, false);
});

test("ignores exact declarations embedded in Rust fixture strings", () => {
  const stale = declarations.replace('"native-key"', '"stale-key"');
  const fixture = `r###"\npub const ROOT_PUBLIC_KEY_BASE64: &str = "native-key";\n"###`;
  assert.equal(parseNativeTrustRoot(`${stale}\nconst FIXTURE: &str = ${fixture};`).publicKey, "stale-key");
});

test("rejects duplicate, missing, and commented-out native declarations", () => {
  assert.throws(() => parseNativeTrustRoot(`${declarations}\npub const ROOT_PRODUCTION_READY: bool = true;`), /exactly one active native ROOT_PRODUCTION_READY/u);
  assert.throws(() => parseNativeTrustRoot(declarations.replace(/pub const ROOT_FINGERPRINT[^;]+;/u, "")), /exactly one active native ROOT_FINGERPRINT/u);
  assert.throws(() => parseNativeTrustRoot(declarations.replace("pub const ROOT_PRODUCTION_READY: bool = true;", "/* pub const ROOT_PRODUCTION_READY: bool = true; */")), /exactly one active native ROOT_PRODUCTION_READY/u);
});
