// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import test from "node:test";
import { createHash } from "node:crypto";
import { mkdtempSync, writeFileSync, rmSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { reconcile, prepare } from "./ccow-publish.mjs";
import { checks, lanes, phases, gate } from "./ccow-acceptance.mjs";
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
    packages: ["@delino/ccow-linux-x64", "@delino/ccow"].map((name) => {
      const bytes = Buffer.from(name);
      return {
        name,
        version,
        revision,
        bytes,
        integrity: `sha512-${createHash("sha512").update(bytes).digest("base64")}`,
      };
    }),
  };
  const state = {
    tag: null,
    releases: [],
    assets: [],
    packages: new Map(),
    latest: new Map(),
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
    package: async (name) => state.packages.get(name) ?? null,
    tarball: async (name) => state.packages.get(name).bytes,
    publish: async (item) => {
      write(`npm:${item.name}`);
      state.packages.set(item.name, {
        name: item.name,
        version,
        gitHead: revision,
        bytes: item.bytes,
        dist: { integrity: item.integrity, tarball: item.name },
      });
    },
    latest: async (name) => state.latest.get(name) ?? null,
    promote: async (item) => {
      write(`latest:${item.name}`);
      state.latest.set(item.name, version);
    },
  };
  return { plan, api, state };
}

test("fresh publication orders native packages before launcher and reads back all bytes", async () => {
  const { plan, api, state } = fixture();
  assert.equal((await reconcile(plan, api)).status, "published");
  assert.deepEqual(state.writes, [
    "tag",
    "draft",
    "asset:archive.tgz",
    "asset:SHA256SUMS",
    "npm:@delino/ccow-linux-x64",
    "npm:@delino/ccow",
    "public",
    "latest:@delino/ccow-linux-x64",
    "latest:@delino/ccow",
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
    "publish",
    "publishRelease",
    "promote",
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
      assert.equal(state.packages.size, 2);
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
    ({ state }) => {
      state.packages.get("@delino/ccow").gitHead = "b".repeat(40);
    },
    ({ state }) => {
      state.packages.get("@delino/ccow").dist.integrity = "wrong";
    },
    ({ state }) => {
      state.packages.get("@delino/ccow").bytes = Buffer.from("conflict");
    },
    ({ api }) => {
      api.package = async () => {
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

test("partial owned drafts upload only missing assets and newer latest never regresses", async () => {
  const { plan, api, state } = fixture();
  await reconcile(plan, api);
  state.releases[0].draft = true;
  state.assets.pop();
  state.latest = new Map(plan.packages.map((p) => [p.name, "0.2.0"]));
  state.writes = [];
  await reconcile(plan, api);
  assert.deepEqual(state.writes, ["asset:SHA256SUMS", "public"]);
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
  assert.equal(f.state.packages.size, 0);
  assert.equal(f.state.releases[0].draft, true);
});

// Construct synthetic packages explicitly for gate-schema testing. Rewriting
// fixture metadata here does not run Go binaries or establish native support.
function acceptedFixture(directory) {
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
    const metaFile =
      artifact.kind === "npm" ? "package/package.json" : "release.json";
    const meta = JSON.parse(files.get(metaFile).bytes);
    if (artifact.kind === "npm") {
      delete meta.private;
      meta.publishConfig = { access: "public" };
      meta.description = "CacheCow native CLI";
      meta.ccow.fixture = false;
    } else meta.fixture = false;
    files.get(metaFile).bytes = Buffer.from(JSON.stringify(meta));
    const bytes = archive(Object.fromEntries(files));
    writeFileSync(path.join(artifacts, artifact.name), bytes);
    artifact.sha256 = sha256(bytes);
  }
  writeFileSync(path.join(artifacts, "release.json"), JSON.stringify(release));
  writeFileSync(
    path.join(artifacts, "SHA256SUMS"),
    release.artifacts.map((a) => `${a.sha256}  ${a.name}\n`).join(""),
  );
  const records = Object.entries(lanes).map(([lane, environment]) => ({
    schema: 1,
    lane,
    revision,
    version: "0.1.0",
    status: "passed",
    kind: "native",
    artifactDigest: sha256(readFileSync(path.join(artifacts, "SHA256SUMS"))),
    environment: {
      ...environment,
      cargo: "1.99.1",
      node: "24.20.0",
      osVersion: lane.startsWith("macos")
        ? "15.0"
        : lane.startsWith("ubuntu")
          ? "Ubuntu 22.04"
          : "Windows 11 24H2 (26100)",
    },
    checks: Object.fromEntries(checks.map((c) => [c, true])),
    tools: { cargo: "1.99.1", rspack: "2.3.0", rsbuild: "2.3.0" },
    unperformed: [],
    measurements: ["cargo", "rspack", "rsbuild"].flatMap((preset) =>
      phases.map((phase) => ({
        preset,
        phase,
        physicalBytes: 1,
        preparationMs: 1,
        buildMs: 1,
        peakMemoryBytes: 1,
      })),
    ),
  }));
  const save = () =>
    records.forEach((r) =>
      writeFileSync(path.join(directory, `${r.lane}.json`), JSON.stringify(r)),
    );
  save();
  return {
    artifacts,
    evidence: directory,
    revision,
    version: "0.1.0",
    ref: "refs/heads/main",
    records,
    save,
  };
}

test("publication gate binds all lanes, minimum hosts, revision, version and candidate bytes", () => {
  const directory = mkdtempSync(path.join(tmpdir(), "ccow-publisher-fixture-"));
  try {
    const f = acceptedFixture(directory);
    const plan = prepare(f);
    assert.equal(plan.packages.at(-1).name, "@delino/ccow");
    assert.equal(plan.assets.size, 10);
    for (const mutate of [
      (r) => {
        r.revision = "b".repeat(40);
      },
      (r) => {
        r.version = "0.2.0";
      },
      (r) => {
        r.artifactDigest = "b".repeat(64);
      },
      (r) => {
        r.status = "blocked";
      },
      (r) => {
        r.checks.rollback = false;
      },
      (r) => {
        r.environment.osVersion = "macOS 16";
      },
      (r) => {
        r.environment.node = "25.0.0";
      },
      (r) => {
        r.environment.cargo = "1.100.0";
      },
    ]) {
      const original = structuredClone(f.records[0]);
      mutate(f.records[0]);
      f.save();
      assert.throws(() => prepare(f));
      f.records[0] = original;
      f.save();
    }
    f.records[0].secret = "private-path";
    f.save();
    assert(
      !prepare(f).assets.get("native-acceptance.json").includes("private-path"),
    );
    rmSync(path.join(directory, "ubuntu22-xfs.json"));
    assert.throws(() => gate(directory, f.artifacts, f.revision));
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("transport paginates releases/assets, pins destinations and passes npm paths as argv", async () => {
  const { transport } = await import("./ccow-publish.mjs");
  const requests = [],
    commands = [];
  const api = transport({
    request: (endpoint, options) => {
      requests.push({ endpoint, options });
      if (endpoint.endsWith("&page=1"))
        return Array.from({ length: 100 }, (_, id) => ({ id }));
      if (endpoint.endsWith("&page=2")) return [{ id: 100 }];
      return {};
    },
    spawn: (command, args, options) => {
      commands.push({ command, args, options });
      return { status: 0 };
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
  await api.upload(7, "archive.tgz", Buffer.from("asset"));
  assert.equal(
    requests.at(-1).endpoint,
    "https://uploads.github.com/repos/delinoio/oss/releases/7/assets?name=archive.tgz",
  );
  await api.publish({
    file: "/tmp/path with spaces/package.tgz",
    version: "0.1.0",
  });
  assert.deepEqual(commands[0].args, [
    "publish",
    "/tmp/path with spaces/package.tgz",
    "--access",
    "public",
    "--tag",
    "ccow-v0.1.0",
    "--registry",
    "https://registry.npmjs.org",
    "--ignore-scripts",
  ]);
  assert.equal(commands[0].options.shell, undefined);
});

test("registry absence is only HTTP 404; errors, unsafe tarball URLs and failed writes stop", async () => {
  const { transport } = await import("./ccow-publish.mjs");
  let status = 404;
  const api = transport({
    fetcher: async () => new Response("{}", { status }),
    spawn: () => ({ status: 1 }),
  });
  assert.equal(await api.package("@delino/ccow", "0.1.0"), null);
  for (status of [401, 403, 429, 500, 503])
    await assert.rejects(api.package("@delino/ccow", "0.1.0"));
  for (const url of [
    "https://attacker.example/package.tgz",
    "http://registry.npmjs.org/package.tgz",
    "https://user:secret@registry.npmjs.org/package.tgz",
    "https://registry.npmjs.org/package.tgz?token=value",
  ])
    await assert.rejects(api.tarball(url));
  await assert.rejects(
    api.publish({ file: "/tmp/package.tgz", version: "0.1.0" }),
  );
});
