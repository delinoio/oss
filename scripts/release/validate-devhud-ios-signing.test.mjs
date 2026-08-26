import assert from "node:assert/strict";
import test from "node:test";

import { iosBundles, validateIosSigningPolicy } from "./validate-devhud-ios-signing.mjs";

const teamId = "ABCDE12345";
const prefix = teamId;
const certificate = Buffer.from("apple-distribution-certificate");
const now = new Date("2026-08-25T00:00:00Z");

function validPolicy(bundle = iosBundles[0]) {
  const applicationIdentifier = `${prefix}.${bundle.bundleId}`;
  return {
    bundle,
    actualBundleId: bundle.bundleId,
    teamId,
    now,
    profile: {
      ApplicationIdentifierPrefix: [prefix],
      DeveloperCertificates: [certificate.toString("base64")],
      Entitlements: {
        "application-identifier": applicationIdentifier,
        "com.apple.developer.team-identifier": teamId,
        "get-task-allow": false,
      },
      ExpirationDate: "2027-08-25T00:00:00Z",
      TeamIdentifier: [teamId],
    },
    signedEntitlements: {
      "application-identifier": applicationIdentifier,
      "com.apple.developer.team-identifier": teamId,
      "com.apple.security.application-groups": [...bundle.applicationGroups],
      "get-task-allow": false,
      "keychain-access-groups": bundle.keychainSuffixes.map((suffix) => `${prefix}.${suffix}`),
    },
    signer: {
      raw: certificate,
      subject: `CN=Apple Distribution: DevHud (${teamId})\nOU=${teamId}\nO=DevHud`,
      validFrom: "2025-08-25T00:00:00Z",
      validTo: "2027-08-25T00:00:00Z",
    },
  };
}

test("accepts App Store signing policy for the app and both extensions", () => {
  for (const bundle of iosBundles) assert.doesNotThrow(() => validateIosSigningPolicy(validPolicy(bundle)));
});

test("rejects development, ad-hoc, and enterprise provisioning profiles", () => {
  const development = validPolicy();
  development.profile.ProvisionedDevices = ["device"];
  development.profile.Entitlements["get-task-allow"] = true;
  assert.throws(() => validateIosSigningPolicy(development), /not App Store|get-task-allow/u);

  const enterprise = validPolicy();
  enterprise.profile.ProvisionsAllDevices = true;
  assert.throws(() => validateIosSigningPolicy(enterprise), /enterprise/u);
});

test("rejects wrong team, bundle, entitlement, and signer bindings", () => {
  const wrongTeam = validPolicy();
  wrongTeam.profile.TeamIdentifier = ["ZYXWV98765"];
  assert.throws(() => validateIosSigningPolicy(wrongTeam), /team/u);

  const wrongBundle = validPolicy();
  wrongBundle.actualBundleId = "io.delino.other";
  assert.throws(() => validateIosSigningPolicy(wrongBundle), /bundle identifier/u);

  const debugEntitlement = validPolicy();
  debugEntitlement.signedEntitlements["get-task-allow"] = true;
  assert.throws(() => validateIosSigningPolicy(debugEntitlement), /get-task-allow/u);

  const wrongSigner = validPolicy();
  wrongSigner.signer.raw = Buffer.from("other-certificate");
  assert.throws(() => validateIosSigningPolicy(wrongSigner), /not included/u);
});

test("rejects expired profiles and non-distribution certificates", () => {
  const expired = validPolicy();
  expired.profile.ExpirationDate = "2026-08-24T23:59:59Z";
  assert.throws(() => validateIosSigningPolicy(expired), /expired/u);

  const developmentCertificate = validPolicy();
  developmentCertificate.signer.subject = `CN=Apple Development: DevHud (${teamId})\nOU=${teamId}\nO=DevHud`;
  assert.throws(() => validateIosSigningPolicy(developmentCertificate), /Apple Distribution/u);
});
