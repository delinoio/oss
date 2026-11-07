// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { spawnSync, execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { releaseIdentity } from "./ccow-release-identity.mjs";
import {
  identity,
  verifyRelease,
  sha256,
} from "../../packages/ccow/scripts/package.mjs";

const repository = "delinoio/oss";

// Only this constructor admits publication inputs. It verifies the exact source
// and non-fixture native archives before any remote operation.
export function prepare({ artifacts, revision, version, ref }) {
  const id = releaseIdentity(version, revision, ref);
  const release = verifyRelease(artifacts, identity(revision, false));
  const assets = new Map(
    [...release.artifacts.map((a) => a.name), "release.json", "SHA256SUMS"].map(
      (name) => [name, readFileSync(path.join(artifacts, name))],
    ),
  );
  return {
    ...id,
    assets,
    marker: `CacheCow release\nSource: ${revision}\nCandidate SHA256: ${sha256(assets.get("SHA256SUMS"))}\n`,
  };
}

async function exactTag(api, plan, create = false) {
  let object = await api.tag(plan.tag);
  if (!object && create) {
    await api.createTag(plan.tag, plan.revision);
    object = await api.tag(plan.tag);
  }
  if (!object) return false;
  const visited = new Set();
  for (let depth = 0; object.type === "tag"; depth++) {
    assert(
      depth < 8 && !visited.has(object.sha),
      "Invalid annotated tag chain",
    );
    visited.add(object.sha);
    object = await api.tagObject(object.sha);
  }
  assert.equal(object.type, "commit", "Release tag must identify a commit");
  assert.equal(
    object.sha,
    plan.revision,
    "Release tag source conflict; never move tags",
  );
  return true;
}

async function checkAssets(api, release, plan, complete) {
  const remote = await api.assets(release.id);
  const names = new Set();
  for (const asset of remote) {
    assert(!names.has(asset.name), "Duplicate release asset");
    names.add(asset.name);
    assert(plan.assets.has(asset.name), "Unexpected release asset");
    assert.equal(
      asset.state,
      "uploaded",
      "Incomplete remote asset; inspect before retry",
    );
    assert.deepEqual(
      await api.asset(asset.id),
      plan.assets.get(asset.name),
      "Release asset bytes conflict",
    );
  }
  const missing = [...plan.assets.keys()].filter((name) => !names.has(name));
  if (complete)
    assert.equal(missing.length, 0, "Public release has missing assets");
  return missing;
}

// The transport is injected for failure/retry fixtures. The executable always
// constructs the plan through prepare; fixture packages cannot reach production.
export async function reconcile(plan, api) {
  api.event?.("remote-preflight");
  const tagged = await exactTag(api, plan);
  const matches = (await api.releases()).filter((r) => r.tag_name === plan.tag);
  assert(matches.length <= 1, "Ambiguous release identity");
  let release = matches[0];
  if (release) {
    assert(tagged, "Orphan release without an exact tag");
    assert.equal(release.body, plan.marker, "Release ownership conflict");
    assert.equal(release.prerelease, false, "Unexpected prerelease");
    await checkAssets(api, release, plan, !release.draft);
  }
  // The preflight above checked all existing remote state before the first write.
  api.event?.("tag-and-draft");
  assert(await exactTag(api, plan, true), "Release tag creation not visible");
  if (!release) release = await api.createRelease(plan);
  assert.equal(release.tag_name, plan.tag);
  assert.equal(release.body, plan.marker);
  assert.equal(release.prerelease, false);
  const releaseId = release.id;
  const missing = await checkAssets(api, release, plan, !release.draft);
  api.event?.("asset-reconciliation");
  for (const name of missing) {
    assert(await exactTag(api, plan), "Release tag disappeared");
    await api.upload(releaseId, name, plan.assets.get(name));
  }
  await checkAssets(api, release, plan, true);
  // Re-read the pinned release, not an untrusted URL or a moving tag lookup.
  release = await api.release(releaseId);
  assert.equal(release.tag_name, plan.tag);
  assert.equal(release.body, plan.marker);
  assert.equal(release.prerelease, false);
  await checkAssets(api, release, plan, true);
  assert(await exactTag(api, plan), "Release tag disappeared");
  api.event?.("release-publication");
  if (release.draft) await api.publishRelease(releaseId);
  const published = await api.release(releaseId);
  assert.equal(published.draft, false, "Release publication not visible");
  assert.equal(published.tag_name, plan.tag);
  assert.equal(published.body, plan.marker);
  await checkAssets(api, published, plan, true);
  assert(await exactTag(api, plan), "Release tag disappeared");
  return {
    revision: plan.revision,
    version: plan.version,
    tag: plan.tag,
    status: "published",
  };
}

// gh owns GitHub authentication. Do not echo child stderr: remote errors can
// contain credentials or private local configuration.
export function githubRequest(
  endpoint,
  { method = "GET", data, bytes, optional = false, binary = false } = {},
) {
  const args = ["api", "--include", "--method", method, endpoint];
  if (binary) args.push("-H", "Accept: application/octet-stream");
  if (bytes)
    args.push("-H", "Content-Type: application/octet-stream", "--input", "-");
  else if (data !== undefined) args.push("--input", "-");
  const result = spawnSync("gh", args, {
    input: bytes ?? (data === undefined ? undefined : JSON.stringify(data)),
    maxBuffer: 256 * 1024 * 1024,
    stdio: ["pipe", "pipe", "pipe"],
  });
  assert(!result.error, "GitHub transport failed");
  const output = result.stdout;
  const end =
    output.indexOf("\r\n\r\n") >= 0
      ? output.indexOf("\r\n\r\n")
      : output.indexOf("\n\n");
  assert(end >= 0, "Missing GitHub response headers");
  const header = output.subarray(0, end).toString();
  const status = Number(/^HTTP\/\S+ (\d+)/u.exec(header)?.[1]);
  if (optional && status === 404) return null;
  assert(
    status >= 200 && status < 300 && result.status === 0,
    `GitHub request failed (${status})`,
  );
  const body = output.subarray(end + (output[end] === 13 ? 4 : 2));
  return binary ? body : body.length ? JSON.parse(body) : null;
}

export function transport({ request = githubRequest } = {}) {
  const base = `repos/${repository}`;
  const api = (endpoint, options) => request(`${base}/${endpoint}`, options);
  const pages = async (endpoint) => {
    const all = [];
    const seen = new Set();
    for (let page = 1; ; page++) {
      const items = api(`${endpoint}?per_page=100&page=${page}`);
      assert(Array.isArray(items));
      for (const item of items) {
        assert(
          Number.isSafeInteger(item.id) && !seen.has(item.id),
          "Invalid or repeated paginated identity",
        );
        seen.add(item.id);
      }
      all.push(...items);
      if (items.length < 100) return all;
    }
  };
  return {
    event: (phase) =>
      console.log(JSON.stringify({ event: "ccow_publication", phase })),
    tag: async (tag) =>
      api(`git/ref/tags/${encodeURIComponent(tag)}`, { optional: true })
        ?.object,
    tagObject: async (sha) => api(`git/tags/${sha}`).object,
    createTag: async (tag, sha) =>
      api("git/refs", {
        method: "POST",
        data: { ref: `refs/tags/${tag}`, sha },
      }),
    releases: () => pages("releases"),
    createRelease: async (plan) =>
      api("releases", {
        method: "POST",
        data: {
          tag_name: plan.tag,
          target_commitish: plan.revision,
          name: `CacheCow ${plan.version}`,
          body: plan.marker,
          draft: true,
          prerelease: false,
        },
      }),
    release: async (id) => api(`releases/${id}`),
    assets: (id) => pages(`releases/${id}/assets`),
    asset: async (id) => api(`releases/assets/${id}`, { binary: true }),
    upload: async (id, name, bytes) =>
      request(
        `https://uploads.github.com/${base}/releases/${id}/assets?name=${encodeURIComponent(name)}`,
        { method: "POST", bytes },
      ),
    publishRelease: async (id) =>
      api(`releases/${id}`, {
        method: "PATCH",
        data: { draft: false, make_latest: "false" },
      }),
  };
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    assert.equal(process.env.GITHUB_REPOSITORY, repository);
    assert.equal(process.env.GITHUB_EVENT_NAME, "workflow_dispatch");
    assert(process.env.GH_TOKEN, "Publication credentials missing");
    const [artifacts, version] = process.argv.slice(2);
    const revision = process.env.GITHUB_SHA;
    assert.equal(
      execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8" }).trim(),
      revision,
    );
    assert.equal(
      execFileSync("git", ["status", "--porcelain"], {
        encoding: "utf8",
      }).trim(),
      "",
    );
    const plan = prepare({
      artifacts,
      version,
      revision,
      ref: process.env.GITHUB_REF,
    });
    console.log(JSON.stringify(await reconcile(plan, transport())));
  } catch {
    // Never print assertions containing artifact bytes or remote response metadata.
    console.error(
      "CacheCow publication failed. Inspect remote identity and rerun the original failed jobs; never replace conflicting tags or assets.",
    );
    process.exitCode = 1;
  }
}
