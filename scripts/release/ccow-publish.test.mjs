// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import test from "node:test";
import { mkdtempSync, writeFileSync, rmSync, readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { tmpdir } from "node:os";
import path from "node:path";
import { reconcile, prepare, transport } from "./ccow-publish.mjs";
import {
  packageRelease,
  sha256,
} from "../../packages/ccow/scripts/package.mjs";
import platforms from "../../packages/ccow/src/platforms.cjs";
import { archive, entries } from "../../packages/ccow/scripts/archive.mjs";

// These are state-machine fixtures, not native acceptance or registry writes.
function fixture() {
  const revision = "a".repeat(40),
    version = "0.1.0";
  const plan = {
    revision,
    version,
    tag: `ccow@v${version}`,
    marker: "owned source and digest",
    assets: new Map([
      ["archive.tgz", Buffer.from("fixture")],
      ["SHA256SUMS", Buffer.from("checksums")],
    ]),
  };
  const state = {
    tag: null,
    releases: [],
    assets: [],
    writes: [],
    next: 1,
  };
  const write = (event) => state.writes.push(event);
  const api = {
    tag: async () => state.tag,
    tagObject: async () => ({ type: "commit", sha: revision }),
    createTag: async (_, sha) => {
      write("tag");
      state.tag = { type: "commit", sha };
    },
    releases: async () => state.releases,
    createRelease: async () => {
      write("draft");
      const r = {
        id: 7,
        tag_name: plan.tag,
        body: plan.marker,
        draft: true,
        prerelease: false,
      };
      state.releases.push(r);
      return r;
    },
    release: async (id) => {
      assert.equal(id, 7);
      return state.releases[0];
    },
    assets: async () => state.assets,
    asset: async (id) => state.assets.find((a) => a.id === id).bytes,
    upload: async (id, name, bytes) => {
      assert.equal(id, 7);
      write(`asset:${name}`);
      state.assets.push({ id: state.next++, name, bytes, state: "uploaded" });
    },
    publishRelease: async () => {
      write("public");
      state.releases[0].draft = false;
    },
  };
  return { plan, api, state };
}

test("fresh publication creates tag and draft, uploads assets and reads back all bytes", async () => {
  const { plan, api, state } = fixture();
  assert.equal((await reconcile(plan, api)).status, "published");
  assert.deepEqual(state.writes, [
    "tag",
    "draft",
    "asset:archive.tgz",
    "asset:SHA256SUMS",
    "public",
  ]);
  state.writes = [];
  await reconcile(plan, api);
  assert.deepEqual(state.writes, []);
});

test("retry reconciles every interruption, including writes whose response was lost", async () => {
  for (const method of [
    "createTag",
    "createRelease",
    "upload",
    "publishRelease",
  ]) {
    for (const after of [false, true]) {
      const { plan, api, state } = fixture();
      const original = api[method];
      api[method] = async (...args) => {
        if (after) await original(...args);
        throw new Error("connection lost");
      };
      await assert.rejects(reconcile(plan, api));
      api[method] = original;
      await reconcile(plan, api);
      assert.equal(state.releases[0].draft, false);
      assert.equal(new Set(state.assets.map((a) => a.name)).size, 2);
    }
  }
});

test("conflicts and uncertain reads prevent all writes", async () => {
  const mutations = [
    ({ state }) => {
      state.tag.sha = "b".repeat(40);
    },
    ({ state }) => {
      state.tag = null;
    },
    ({ state }) => {
      state.releases.push({ ...state.releases[0], id: 8 });
    },
    ({ state }) => {
      state.releases[0].body = "another owner";
    },
    ({ state }) => {
      state.releases[0].prerelease = true;
    },
    ({ state }) => {
      state.assets[0].bytes = Buffer.from("conflict");
    },
    ({ state }) => {
      state.assets.push({ ...state.assets[0], id: 99 });
    },
    ({ state }) => {
      state.assets[0].state = "starter";
    },
    ({ state }) => {
      state.assets[0].name = "unexpected";
    },
    ({ state }) => {
      state.assets.pop();
    },
    ({ api }) => {
      api.releases = async () => {
        throw new Error("503");
      };
    },
    ({ api }) => {
      api.assets = async () => {
        throw new Error("403");
      };
    },
  ];
  for (const mutate of mutations) {
    const f = fixture();
    await reconcile(f.plan, f.api);
    f.state.writes = [];
    mutate(f);
    await assert.rejects(reconcile(f.plan, f.api));
    assert.deepEqual(f.state.writes, []);
  }
});

test("partial owned drafts upload only missing assets and never clobber or delete", async () => {
  const { plan, api, state } = fixture();
  await reconcile(plan, api);
  state.releases[0].draft = true;
  const kept = state.assets[0];
  state.assets.pop();
  state.writes = [];
  await reconcile(plan, api);
  assert.deepEqual(state.writes, ["asset:SHA256SUMS", "public"]);
  assert.equal(state.assets[0], kept);
});

test("a public release with missing assets fails closed without writes", async () => {
  const { plan, api, state } = fixture();
  await reconcile(plan, api);
  state.assets.pop();
  state.writes = [];
  await assert.rejects(reconcile(plan, api), /missing assets/);
  assert.deepEqual(state.writes, []);
});

test("annotated exact tags work; cycles, wrong objects and moved tags fail", async () => {
  const f = fixture();
  f.state.tag = { type: "tag", sha: "b".repeat(40) };
  await reconcile(f.plan, f.api);
  f.state.writes = [];
  f.api.tagObject = async () => f.state.tag;
  await assert.rejects(reconcile(f.plan, f.api));
  assert.deepEqual(f.state.writes, []);
  f.state.tag = { type: "tree", sha: f.plan.revision };
  await assert.rejects(reconcile(f.plan, f.api));
});

test("tag movement during upload stops publication", async () => {
  const f = fixture();
  const upload = f.api.upload;
  f.api.upload = async (...args) => {
    await upload(...args);
    f.state.tag.sha = "b".repeat(40);
  };
  await assert.rejects(reconcile(f.plan, f.api));
  assert.equal(f.state.releases[0].draft, true);
});

test("the draft is published only after all assets read back", async () => {
  const f = fixture();
  f.api.event = (phase) => f.state.writes.push(`event:${phase}`);
  const upload = f.api.upload;
  f.api.upload = async (...args) => {
    await upload(...args);
    if (f.state.assets.length === 2) f.state.assets[0].bytes = Buffer.from("x");
  };
  await assert.rejects(reconcile(f.plan, f.api), /bytes conflict/);
  assert.equal(f.state.releases[0].draft, true);
  assert(!f.state.writes.includes("public"));
});

// Construct synthetic archives explicitly for publication-input testing.
// Rewriting fixture metadata here does not run native binaries.
function candidateFixture(directory) {
  const revision = "a".repeat(40);
  const binaries = Object.fromEntries(
    platforms.targets.map((t) => {
      const file = path.join(directory, t.suffix);
      writeFileSync(file, "non-native test payload");
      return [t.suffix, file];
    }),
  );
  const artifacts = path.join(directory, "candidate");
  const release = packageRelease({
    binaries,
    output: artifacts,
    sourceRevision: revision,
    fixture: true,
  });
  release.fixture = false;
  for (const artifact of release.artifacts) {
    const files = entries(readFileSync(path.join(artifacts, artifact.name)));
    const meta = JSON.parse(files.get("release.json").bytes);
    meta.fixture = false;
    files.get("release.json").bytes = Buffer.from(JSON.stringify(meta));
    const bytes = archive(Object.fromEntries(files));
    writeFileSync(path.join(artifacts, artifact.name), bytes);
    artifact.sha256 = sha256(bytes);
  }
  writeFileSync(path.join(artifacts, "release.json"), JSON.stringify(release));
  writeFileSync(
    path.join(artifacts, "SHA256SUMS"),
    release.artifacts.map((a) => `${a.sha256}  ${a.name}\n`).join(""),
  );
  return {
    artifacts,
    revision,
    version: "0.1.0",
    ref: "refs/heads/main",
  };
}

test("publication binds the exact revision, version, ref and candidate bytes", () => {
  const directory = mkdtempSync(path.join(tmpdir(), "ccow-publisher-fixture-"));
  try {
    const f = candidateFixture(directory);
    const plan = prepare(f);
    assert.deepEqual(
      [...plan.assets.keys()].sort(),
      [
        ...platforms.targets.map((t) => `ccow-0.1.0-${t.suffix}.tar.gz`),
        "SHA256SUMS",
        "release.json",
      ].sort(),
    );
    assert.equal(plan.packages, undefined);
    assert(!plan.assets.has("native-acceptance.json"));
    for (const mutate of [
      { revision: "b".repeat(40) },
      { version: "0.2.0" },
      { ref: "refs/heads/topic" },
    ])
      assert.throws(() => prepare({ ...f, ...mutate }));
    writeFileSync(path.join(f.artifacts, "SHA256SUMS"), "tampered\n");
    assert.throws(() => prepare(f));
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("publication rejects npm leftovers in the candidate directory", () => {
  const directory = mkdtempSync(path.join(tmpdir(), "ccow-publisher-fixture-"));
  try {
    const f = candidateFixture(directory);
    writeFileSync(path.join(f.artifacts, "delino-ccow-0.1.0.tgz"), "npm");
    assert.throws(() => prepare(f), /Unexpected/);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("transport paginates releases/assets and pins destinations without npm operations", async () => {
  const requests = [];
  const api = transport({
    request: (endpoint, options) => {
      requests.push({ endpoint, options });
      if (endpoint.endsWith("&page=1"))
        return Array.from({ length: 100 }, (_, id) => ({ id }));
      if (endpoint.endsWith("&page=2")) return [{ id: 100 }];
      return {};
    },
  });
  assert.equal((await api.releases()).length, 101);
  assert.equal((await api.assets(7)).length, 101);
  await api.createTag("ccow@v0.1.0", "a".repeat(40));
  assert.deepEqual(requests.at(-1), {
    endpoint: "repos/delinoio/oss/git/refs",
    options: {
      method: "POST",
      data: { ref: "refs/tags/ccow@v0.1.0", sha: "a".repeat(40) },
    },
  });
  await api.createRelease({
    tag: "ccow@v0.1.0",
    revision: "a".repeat(40),
    version: "0.1.0",
    marker: "marker",
  });
  assert.equal(requests.at(-1).options.data.draft, true);
  assert.equal(requests.at(-1).options.data.target_commitish, "a".repeat(40));
  await api.upload(7, "archive.tgz", Buffer.from("asset"));
  assert.equal(
    requests.at(-1).endpoint,
    "https://uploads.github.com/repos/delinoio/oss/releases/7/assets?name=archive.tgz",
  );
  await api.publishRelease(7);
  assert.deepEqual(requests.at(-1).options.data, {
    draft: false,
    make_latest: "false",
  });
  for (const name of ["package", "tarball", "publish", "latest", "promote"])
    assert.equal(api[name], undefined, `${name} must not exist`);
});

test("the executable requires the GitHub release token and ignores npm credentials", () => {
  const result = spawnSync(
    process.execPath,
    [
      new URL("./ccow-publish.mjs", import.meta.url).pathname,
      "unused",
      "0.1.0",
    ],
    {
      env: {
        PATH: process.env.PATH,
        GITHUB_REPOSITORY: "delinoio/oss",
        GITHUB_EVENT_NAME: "workflow_dispatch",
        NODE_AUTH_TOKEN: "unrelated",
      },
      encoding: "utf8",
    },
  );
  assert.equal(result.status, 1);
  assert.doesNotMatch(result.stderr, /npm/iu);
});
