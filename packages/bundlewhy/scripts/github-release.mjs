import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { createHash } from "node:crypto";
import { ensure, event, isMain, packageRoot, revision } from "./common.mjs";
import { verifySet } from "./package.mjs";
import { publish } from "../../clibox/scripts/github-release.mjs";
import { verifyBundle } from "../../../scripts/release/linux-packages/release-input.mjs";

export function stage(directory, output, sourceRevision) {
  const artifacts = verifySet(directory, sourceRevision);
  const version = artifacts[0].version;
  const plan = {
    project: "bundlewhy",
    version,
    revision: sourceRevision,
    tag: `bundlewhy@v${version}`,
  };
  const files = new Map(
    artifacts.map((artifact) => [
      artifact.filename,
      readFileSync(path.join(directory, artifact.filename)),
    ]),
  );
  files.set(
    "SHA256SUMS",
    Buffer.from(
      [...files]
        .map(
          ([name, bytes]) =>
            `${createHash("sha256").update(bytes).digest("hex")}  ${name}\n`,
        )
        .join(""),
    ),
  );
  mkdirSync(output, { recursive: true });
  for (const [name, bytes] of files)
    writeFileSync(path.join(output, name), bytes);
  event("github_stage", {
    version,
    revision: sourceRevision,
    assets: [...files.keys()],
  });
  return { plan, files };
}

export async function main() {
  ensure(
    process.argv.slice(2).every((arg) => arg === "--publish") &&
      process.argv.length <= 3,
    "Unknown release argument",
  );
  const output = path.join(packageRoot, "dist/github");
  const candidate = stage(
    path.join(packageRoot, "dist/tarballs"),
    output,
    revision(),
  );
  if (!process.argv.includes("--publish")) return;
  const { plan } = candidate;
  ensure(
    process.env.GITHUB_REPOSITORY === "delinoio/oss" &&
      process.env.GITHUB_REF === `refs/tags/${plan.tag}` &&
      process.env.GITHUB_SHA === plan.revision,
    "Publication requires the exact first-party tag and commit",
  );
  ensure(
    process.env.GH_TOKEN && process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN,
    "GitHub token and Actions OIDC are required",
  );
  const request = async (url, options = {}, allowMissing = false) => {
    const response = await fetch(url, {
      ...options,
      headers: {
        Authorization: `Bearer ${process.env.GH_TOKEN}`,
        Accept: "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
        ...options.headers,
      },
      signal: AbortSignal.timeout(60000),
      redirect: "error",
    });
    if (allowMissing && response.status === 404) return null;
    ensure(
      response.ok,
      `GitHub release request failed: HTTP ${response.status}`,
    );
    return response;
  };
  await publish(candidate, {
    report: event,
    api: async (method, endpoint, body, missing) =>
      (
        await request(
          `https://api.github.com${endpoint}`,
          {
            method,
            body: body ? JSON.stringify(body) : undefined,
            headers: { "Content-Type": "application/json" },
          },
          missing,
        )
      )?.json() ?? null,
    // gh handles GitHub's authenticated asset redirects without forwarding our
    // authorization header to an arbitrary redirect destination.
    download: (asset) =>
      execFileSync(
        "gh",
        [
          "api",
          `repos/delinoio/oss/releases/assets/${asset.id}`,
          "-H",
          "Accept: application/octet-stream",
        ],
        { maxBuffer: 300 * 1024 * 1024 },
      ),
    upload: (release, name, bytes) =>
      request(
        `https://uploads.github.com/repos/delinoio/oss/releases/${release.id}/assets?name=${encodeURIComponent(name)}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/octet-stream" },
          body: bytes,
        },
      ),
    sign: (name) => {
      const file = path.join(output, name);
      execFileSync(
        "cosign",
        ["sign-blob", "--yes", "--bundle", `${file}.sigstore.json`, file],
        { stdio: "inherit" },
      );
      return readFileSync(`${file}.sigstore.json`);
    },
    verify: (name, bytes, bundle) => {
      const file = path.join(output, name);
      writeFileSync(file, bytes);
      writeFileSync(`${file}.sigstore.json`, bundle);
      verifyBundle(file, `${file}.sigstore.json`, plan);
    },
  });
}
if (isMain(import.meta.url))
  main().catch((error) => {
    console.error(
      JSON.stringify({
        event: "bundlewhy_github_release_failed",
        message: error.message,
      }),
    );
    process.exitCode = 1;
  });
