// SPDX-License-Identifier: Apache-2.0
import { X509Certificate } from "node:crypto";
import {
  constants,
  closeSync,
  copyFileSync,
  cpSync,
  existsSync,
  fstatSync,
  fsyncSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  openSync,
  readFileSync,
  renameSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnDevServer } from "../../../scripts/spawn-dev-server.mjs";
import { dryRunEnvironment } from "./bundle-macos-dry-run.mjs";

export const developmentServerIdentifier =
  "io.delino.delidev.development.server";
export const developmentCertificateName = "DeliDev Local Development";
export const appleDevelopmentCertificateName = "Apple Development:";
const identityPattern = /^[A-Fa-f0-9]{40}$/;
const outputLimit = 128 * 1024;

export class DevelopmentSigningError extends Error {
  constructor(code) {
    super(code);
    this.code = code;
  }
}
const fail = (code) => {
  throw new DevelopmentSigningError(code);
};

function privateDirectory(directory) {
  mkdirSync(directory, { recursive: true, mode: 0o700 });
  const stat = lstatSync(directory);
  if (
    !stat.isDirectory() ||
    stat.isSymbolicLink() ||
    stat.uid !== process.getuid() ||
    stat.mode & 0o077
  )
    fail("signing-directory-invalid");
}

export function signingDirectory(home = homedir()) {
  return join(home, "Library/Application Support/delidev-development");
}

export function developmentBundleDirectory(home = homedir()) {
  // Cargo cleanup owns its target directory. Surviving servers/Workers require
  // files outside that build cache until explicit lifecycle cleanup proves exit.
  return join(signingDirectory(home), "bundles");
}

export function readSigningIdentity(directory = signingDirectory()) {
  if (!existsSync(directory)) fail("development-signing-not-configured");
  const stat = lstatSync(directory);
  if (
    !stat.isDirectory() ||
    stat.isSymbolicLink() ||
    stat.uid !== process.getuid() ||
    stat.mode & 0o077
  )
    fail("signing-directory-invalid");
  let fd;
  try {
    fd = openSync(
      join(directory, "signing.json"),
      constants.O_RDONLY | constants.O_NOFOLLOW,
    );
    const file = fstatSync(fd);
    if (
      !file.isFile() ||
      file.uid !== process.getuid() ||
      file.mode & 0o077 ||
      file.size > 2048
    )
      fail("signing-config-invalid");
    const config = JSON.parse(readFileSync(fd, "utf8"));
    if (
      config.schemaVersion !== 1 ||
      !identityPattern.test(config.identity ?? "") ||
      Object.keys(config).sort().join() !== "identity,schemaVersion"
    )
      fail("signing-config-invalid");
    return config.identity.toUpperCase();
  } catch (error) {
    if (error instanceof DevelopmentSigningError) throw error;
    fail(
      error.code === "ENOENT"
        ? "development-signing-not-configured"
        : "signing-config-invalid",
    );
  } finally {
    if (fd !== undefined) closeSync(fd);
  }
}

// All native children use the same lifecycle wrapper as preparation. Output is
// bounded, private, and never echoed: native errors can contain private paths.
export async function signingCommand(
  command,
  args,
  options,
  run = spawnDevServer,
) {
  const temporary = mkdtempSync(join(tmpdir(), "delidev-signing-output-"));
  const output = join(temporary, "stdout");
  const fd = openSync(output, "wx", 0o600);
  try {
    const result = await run(
      command,
      args,
      {
        ...options,
        timeout: 30_000,
        stdio: ["ignore", fd, "ignore"],
        shell: false,
      },
      { terminateProcessTree: true },
    );
    if (result.code !== 0 || result.signal !== null)
      fail(
        result.signal
          ? "development-signing-interrupted"
          : "development-signing-command-failed",
      );
    if (fstatSync(fd).size > outputLimit) fail("signing-output-too-large");
    return readFileSync(output, "utf8");
  } finally {
    closeSync(fd);
    rmSync(temporary, { recursive: true, force: true });
  }
}

export function validateDevelopmentCertificate(
  pem,
  identity,
  now = Date.now(),
) {
  if (!identityPattern.test(identity)) fail("signing-identity-invalid");
  const blocks =
    pem.match(
      /-----BEGIN CERTIFICATE-----[\s\S]*?-----END CERTIFICATE-----/g,
    ) ?? [];
  let selected;
  for (const block of blocks) {
    const certificate = new X509Certificate(block);
    if (
      certificate.fingerprint.replaceAll(":", "").toUpperCase() ===
      identity.toUpperCase()
    )
      selected = certificate;
  }
  if (!selected) fail("development-certificate-missing");
  const names = selected.subject.split("\n");
  const local = names.includes(`CN=${developmentCertificateName}`);
  const appleDevelopment = names.some((name) =>
    name.startsWith(`CN=${appleDevelopmentCertificateName} `),
  );
  // The name only selects a candidate. Registration and publication must also
  // verify Apple's anchor and Mac development extension on the signed code.
  if (
    (!local && !appleDevelopment) ||
    (local &&
      (selected.issuer !== selected.subject ||
        !selected.verify(selected.publicKey))) ||
    !selected.keyUsage?.includes("1.3.6.1.5.5.7.3.3")
  )
    fail("development-certificate-invalid");
  if (
    now < Date.parse(selected.validFrom) ||
    now >= Date.parse(selected.validTo)
  )
    fail("development-certificate-expired");
  return identity.toUpperCase();
}

export async function inspectDevelopmentIdentity(
  identity,
  options,
  command = signingCommand,
) {
  // Search only local/development identities and select the exact registered
  // fingerprint. Never discover or select a distribution/release identity.
  for (const name of [
    developmentCertificateName,
    appleDevelopmentCertificateName,
  ]) {
    const pem = await command(
      "/usr/bin/security",
      ["find-certificate", "-a", "-c", name, "-p"],
      options,
    );
    try {
      return validateDevelopmentCertificate(pem, identity);
    } catch (error) {
      if (
        !(error instanceof DevelopmentSigningError) ||
        error.code !== "development-certificate-missing"
      )
        throw error;
    }
  }
  fail("development-certificate-missing");
}

export function serverSigningArguments(identity, executable) {
  if (!identityPattern.test(identity)) fail("signing-identity-invalid");
  return [
    "--force",
    "--sign",
    identity,
    "--identifier",
    developmentServerIdentifier,
    "--timestamp=none",
    "--options",
    "runtime",
    executable,
  ];
}

export function serverRequirement(identity) {
  if (!identityPattern.test(identity)) fail("signing-identity-invalid");
  // A stable designated requirement alone does not stabilize macOS Keychain
  // partitions for self-signed code: those still use a per-build cdhash. Apple's
  // Mac development certificate produces a team partition. Require its actual
  // Apple chain and development extension, never just a certificate name.
  return `=identifier "${developmentServerIdentifier}" and certificate leaf = H"${identity}" and (certificate leaf[subject.CN] = "${developmentCertificateName}" or (anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.1] exists and certificate leaf[field.1.2.840.113635.100.6.1.12] exists))`;
}

export async function registerDevelopmentIdentity(
  identity,
  {
    directory = signingDirectory(),
    environment = dryRunEnvironment(process.env),
    command = signingCommand,
  } = {},
) {
  identity = await inspectDevelopmentIdentity(
    identity,
    { env: environment },
    command,
  );
  // Prove access to the selected private key during explicit registration.
  // Signing a private copy cannot modify Apple's executable or user key ACLs.
  const probe = mkdtempSync(join(tmpdir(), "delidev-signing-probe-"));
  try {
    const executable = join(probe, "probe");
    copyFileSync("/usr/bin/true", executable);
    await command(
      "/usr/bin/codesign",
      serverSigningArguments(identity, executable),
      { env: environment },
    );
    await command(
      "/usr/bin/codesign",
      ["--verify", "--strict", "-R", serverRequirement(identity), executable],
      { env: environment },
    );
    privateDirectory(directory);
    const temporary = join(directory, `.pending-${process.pid}-${Date.now()}`);
    const fd = openSync(temporary, "wx", 0o600);
    try {
      writeFileSync(fd, `${JSON.stringify({ schemaVersion: 1, identity })}\n`);
      fsyncSync(fd);
    } finally {
      closeSync(fd);
    }
    try {
      renameSync(temporary, join(directory, "signing.json"));
      const parent = openSync(directory, constants.O_RDONLY);
      try {
        fsyncSync(parent);
      } finally {
        closeSync(parent);
      }
    } finally {
      rmSync(temporary, { force: true });
    }
  } finally {
    rmSync(probe, { recursive: true, force: true });
  }
}

export async function publishDevelopmentBundle(
  source,
  output,
  identity,
  options,
  command = signingCommand,
) {
  const sourceStat = lstatSync(source);
  if (!sourceStat.isDirectory() || sourceStat.isSymbolicLink())
    fail("development-bundle-invalid");
  privateDirectory(output);
  const pending = mkdtempSync(join(output, ".pending-"));
  const bundle = join(pending, "DeliDev.app");
  let published = false;
  try {
    cpSync(source, bundle, {
      recursive: true,
      verbatimSymlinks: true,
      mode: constants.COPYFILE_FICLONE,
    });
    const executable = join(bundle, "Contents/MacOS/delidev");
    if (
      !lstatSync(executable).isFile() ||
      lstatSync(executable).isSymbolicLink()
    )
      fail("development-sidecar-invalid");
    await command(
      "/usr/bin/codesign",
      serverSigningArguments(identity, executable),
      options,
    );
    await command(
      "/usr/bin/codesign",
      ["--verify", "--strict", "-R", serverRequirement(identity), executable],
      options,
    );
    // Preserve nested server signatures; --deep signing would replace the stable
    // server identity. Re-seal only the containing app with its original rights.
    await command(
      "/usr/bin/codesign",
      [
        "--force",
        "--sign",
        "-",
        "--timestamp=none",
        "--preserve-metadata=identifier,entitlements,flags,runtime",
        bundle,
      ],
      options,
    );
    await command(
      "/usr/bin/codesign",
      ["--verify", "--deep", "--strict", bundle],
      options,
    );
    const completed = join(
      output,
      `run-${pending.slice(pending.lastIndexOf(".pending-") + 9)}`,
    );
    if (existsSync(completed)) fail("development-publication-conflict");
    renameSync(pending, completed);
    published = true;
    // Never clean a published bundle on desktop exit. Borrowed/crash-surviving
    // servers and independent Workers may still execute these original files.
    return join(completed, "DeliDev.app/Contents/MacOS/delidev-desktop");
  } finally {
    if (!published) rmSync(pending, { recursive: true, force: true });
  }
}

async function main() {
  const args = process.argv.slice(2);
  if (
    args.length !== 2 ||
    args[0] !== "--identity" ||
    !identityPattern.test(args[1])
  ) {
    process.stderr.write(
      `Create an Apple Development certificate in Xcode Settings > Accounts > Manage Certificates, then run dev:signing --identity <40-character certificate SHA-1 fingerprint>. The legacy "${developmentCertificateName}" self-signed identity remains supported but may prompt after each rebuild.\n`,
    );
    process.exitCode = 1;
    return;
  }
  try {
    if (process.platform !== "darwin") fail("development-signing-macos-only");
    await registerDevelopmentIdentity(args[1]);
    process.stdout.write(
      "DeliDev local development signing registered. Authorize existing DeliDev keychain items for the signed development server once before retrying validation.\n",
    );
  } catch (error) {
    process.stderr.write(
      `${JSON.stringify({ operation: "development_signing_registration", state: "failed", code: error instanceof DevelopmentSigningError ? error.code : "development-signing-failed" })}\n`,
    );
    process.exitCode = 1;
  }
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
)
  await main();
