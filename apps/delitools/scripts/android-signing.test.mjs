// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { verifyAndroidSigning } from "./verify-android-signing.mjs";

// This public fixture certificate has no private key in the repository.
const pem = readFileSync(
  new URL("./fixtures/upload-certificate.pem", import.meta.url),
  "utf8",
);
// Independently obtained with openssl x509 -noout -fingerprint -sha256.
const expected =
  "3D:29:0F:BE:59:8F:35:BB:38:6A:7B:7F:95:1C:5F:7A:D1:19:98:85:71:01:DC:E7:00:74:DD:AB:93:CB:C2:F7";

test("Android upload verification accepts the pinned certificate and rejects another identity", () => {
  const run = (command, args) => {
    assert.equal(command, "keytool");
    assert.deepEqual(args, ["-printcert", "-rfc", "-jarfile", "fixture.aab"]);
    return { status: 0, stdout: `Signer #1:\n\n${pem}` };
  };
  assert.equal(
    verifyAndroidSigning("fixture.aab", expected, run),
    expected.replaceAll(":", "").toLowerCase(),
  );
  assert.throws(
    () => verifyAndroidSigning("fixture.aab", "0".repeat(64), run),
    /does not match/,
  );
});

test("Android upload verification fails closed on missing pins, unsigned files and keytool failures", () => {
  for (const value of [undefined, "", "invalid", "a".repeat(63)])
    assert.throws(
      () =>
        verifyAndroidSigning("fixture.aab", value, () =>
          assert.fail("Must reject the pin before keytool"),
        ),
      /Set DELITOOLS_ANDROID_CERTIFICATE_SHA256/,
    );
  assert.throws(
    () =>
      verifyAndroidSigning("fixture.aab", expected, () => ({
        status: 0,
        stdout: "Not a signed jar file",
      })),
    /no signing certificate/,
  );
  assert.throws(
    () =>
      verifyAndroidSigning("fixture.aab", expected, () => ({
        status: 1,
        stdout: pem,
      })),
    /Could not read/,
  );
});
