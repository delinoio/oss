#!/usr/bin/env node

import { X509Certificate } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

export const iosBundles = Object.freeze([
  Object.freeze({
    relativePath: "",
    bundleId: "io.delino.devhud",
    applicationGroups: Object.freeze(["group.io.delino.devhud"]),
    keychainSuffixes: Object.freeze(["io.delino.devhud", "io.delino.devhud.shared"]),
  }),
  Object.freeze({
    relativePath: "PlugIns/DevHUD Deck.appex",
    bundleId: "io.delino.devhud.widget",
    applicationGroups: Object.freeze(["group.io.delino.devhud"]),
    keychainSuffixes: Object.freeze(["io.delino.devhud.shared"]),
  }),
  Object.freeze({
    relativePath: "PlugIns/DevHUD Deck Selection.appex",
    bundleId: "io.delino.devhud.widget.intent",
    applicationGroups: Object.freeze(["group.io.delino.devhud"]),
    keychainSuffixes: Object.freeze([]),
  }),
]);

function requireValue(condition, message) {
  if (!condition) throw new Error(message);
}

function requireStrings(value, message) {
  requireValue(Array.isArray(value) && value.length > 0 && value.every((item) => typeof item === "string" && item !== ""), message);
  return value;
}

function includesEvery(values, expected) {
  return expected.every((value) => values.includes(value));
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&");
}

export function validateIosSigningPolicy({ bundle, actualBundleId, teamId, profile, signedEntitlements, signer, now = new Date() }) {
  const label = bundle.bundleId;
  requireValue(actualBundleId === bundle.bundleId, `${label}: bundle identifier changed to ${actualBundleId}`);
  requireValue(typeof teamId === "string" && /^[A-Z0-9]{10}$/u.test(teamId), `${label}: Apple team ID must contain 10 uppercase letters or digits`);

  const profileTeams = requireStrings(profile.TeamIdentifier, `${label}: provisioning profile has no team identifier`);
  requireValue(profileTeams.includes(teamId), `${label}: provisioning profile team does not match ${teamId}`);
  requireValue(profile.ProvisionsAllDevices !== true, `${label}: enterprise provisioning profiles are not App Store profiles`);
  requireValue(profile.ProvisionedDevices === undefined, `${label}: development and ad-hoc provisioning profiles are not App Store profiles`);
  requireValue(profile.Entitlements?.["get-task-allow"] === false, `${label}: provisioning profile must set get-task-allow=false`);
  requireValue(new Date(profile.ExpirationDate).getTime() > now.getTime(), `${label}: provisioning profile is expired or has no valid expiration`);

  const prefixes = requireStrings(profile.ApplicationIdentifierPrefix, `${label}: provisioning profile has no application identifier prefix`);
  const applicationIdentifier = `${prefixes[0]}.${bundle.bundleId}`;
  requireValue(profile.Entitlements?.["application-identifier"] === applicationIdentifier, `${label}: provisioning profile is not bound to ${applicationIdentifier}`);
  requireValue(profile.Entitlements?.["com.apple.developer.team-identifier"] === teamId, `${label}: provisioning profile entitlement team does not match ${teamId}`);

  requireValue(signedEntitlements?.["application-identifier"] === applicationIdentifier, `${label}: signed application identifier does not match its profile`);
  requireValue(signedEntitlements?.["com.apple.developer.team-identifier"] === teamId, `${label}: signed team identifier does not match its profile`);
  requireValue(signedEntitlements?.["get-task-allow"] === false, `${label}: signed bundle must set get-task-allow=false`);
  if (signedEntitlements?.["aps-environment"] !== undefined) {
    requireValue(signedEntitlements["aps-environment"] === "production", `${label}: signed push entitlement must use the production environment`);
  }

  const applicationGroups = signedEntitlements?.["com.apple.security.application-groups"] ?? [];
  requireValue(Array.isArray(applicationGroups) && includesEvery(applicationGroups, bundle.applicationGroups), `${label}: signed App Group entitlements are incomplete`);
  const keychainGroups = signedEntitlements?.["keychain-access-groups"] ?? [];
  const expectedKeychainGroups = bundle.keychainSuffixes.map((suffix) => `${prefixes[0]}.${suffix}`);
  requireValue(Array.isArray(keychainGroups) && includesEvery(keychainGroups, expectedKeychainGroups), `${label}: signed Keychain entitlements are incomplete`);

  requireValue(Buffer.isBuffer(signer.raw), `${label}: signing certificate is unavailable`);
  const profileCertificates = requireStrings(profile.DeveloperCertificates, `${label}: provisioning profile has no developer certificates`);
  requireValue(profileCertificates.some((certificate) => Buffer.from(certificate, "base64").equals(signer.raw)), `${label}: signing certificate is not included in the provisioning profile`);
  requireValue(new Date(signer.validFrom).getTime() <= now.getTime() && new Date(signer.validTo).getTime() > now.getTime(), `${label}: signing certificate is not currently valid`);
  requireValue(/(?:^|\n)CN=Apple Distribution:/u.test(signer.subject), `${label}: signer is not an Apple Distribution certificate`);
  requireValue(new RegExp(`(?:^|\\n)OU=${escapeRegExp(teamId)}(?:\\n|$)`, "u").test(signer.subject), `${label}: signing certificate team does not match ${teamId}`);
}

function plistJson(input) {
  return JSON.parse(execFileSync("/usr/bin/plutil", ["-convert", "json", "-o", "-", "-"], { input, encoding: "utf8" }));
}

function bundleIdentifier(bundlePath) {
  return execFileSync("/usr/bin/plutil", ["-extract", "CFBundleIdentifier", "raw", join(bundlePath, "Info.plist")], { encoding: "utf8" }).trim();
}

function decodedProfile(bundlePath) {
  const profile = execFileSync("/usr/bin/security", ["cms", "-D", "-i", join(bundlePath, "embedded.mobileprovision")]);
  return plistJson(profile);
}

function signedEntitlements(bundlePath) {
  return plistJson(execFileSync("/usr/bin/codesign", ["-d", "--entitlements", ":-", bundlePath]));
}

function signingCertificate(bundlePath) {
  const directory = mkdtempSync(join(tmpdir(), "devhud-ios-signing-"));
  const prefix = join(directory, "signer");
  try {
    execFileSync("/usr/bin/codesign", ["-d", "--extract-certificates", prefix, bundlePath]);
    const certificate = new X509Certificate(readFileSync(`${prefix}0`));
    return { raw: certificate.raw, subject: certificate.subject, validFrom: certificate.validFrom, validTo: certificate.validTo };
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

export function validateIosApplication(appPath, teamId) {
  requireValue(process.platform === "darwin", "iOS signing validation requires macOS");
  execFileSync("/usr/bin/codesign", ["--verify", "--deep", "--strict", appPath], { stdio: "inherit" });
  for (const bundle of iosBundles) {
    const bundlePath = bundle.relativePath === "" ? appPath : join(appPath, bundle.relativePath);
    execFileSync("/usr/bin/codesign", ["--verify", "--strict", bundlePath], { stdio: "inherit" });
    validateIosSigningPolicy({
      bundle,
      actualBundleId: bundleIdentifier(bundlePath),
      teamId,
      profile: decodedProfile(bundlePath),
      signedEntitlements: signedEntitlements(bundlePath),
      signer: signingCertificate(bundlePath),
    });
  }
}

function parseArguments(arguments_) {
  const options = { app: null, teamId: null };
  for (let index = 0; index < arguments_.length; index += 1) {
    const argument = arguments_[index];
    if (argument === "--app") options.app = arguments_[++index];
    else if (argument === "--team-id") options.teamId = arguments_[++index];
    else throw new Error(`unknown argument: ${argument}`);
  }
  if (!options.app || !options.teamId) throw new Error("usage: validate-devhud-ios-signing.mjs --app <path> --team-id <id>");
  return { app: resolve(options.app), teamId: options.teamId };
}

export function main(arguments_ = process.argv.slice(2)) {
  const options = parseArguments(arguments_);
  validateIosApplication(options.app, options.teamId);
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    main();
  } catch (error) {
    console.error(`[devhud.ios-signing] ${error.message}`);
    process.exit(1);
  }
}
