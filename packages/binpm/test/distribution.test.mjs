// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { gzipSync } from "node:zlib";
import platforms from "../src/platforms.cjs";
import launcher from "../src/launcher.cjs";
import {
  targets,
  metadata,
  releaseIdentity,
  revision,
  sourceText,
} from "../scripts/common.mjs";
import {
  buildPackage,
  inspectTarball,
  packageManifest,
  tarballName,
  tarEntries,
  verifySet,
} from "../scripts/package.mjs";
import {
  publishArtifacts,
  publishTarball,
  registryVersion,
  trustCommands,
} from "../scripts/publish.mjs";

const version = metadata().version;
const sourceRevision = revision();
const tempDirectory = (t) => {
  const directory = mkdtempSync(path.join(tmpdir(), "binpm npm test "));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return directory;
};

// Synthetic tar fixtures establish package validation, not native acceptance.
function archive(files) {
  const chunks = [];
  for (const [name, bytes, mode = 0o644, type = 48] of files) {
    const header = Buffer.alloc(512);
    header.write(`package/${name}`);
    header.write(`${mode.toString(8).padStart(7, "0")}\0`, 100);
    header.write(`${bytes.length.toString(8).padStart(11, "0")}\0`, 124);
    header.fill(32, 148, 156);
    header[156] = type;
    header.write("ustar\0", 257);
    header.write(
      `${header
        .reduce((sum, b) => sum + b, 0)
        .toString(8)
        .padStart(6, "0")}\0 `,
      148,
    );
    chunks.push(
      header,
      bytes,
      Buffer.alloc((512 - (bytes.length % 512)) % 512),
    );
  }
  return gzipSync(Buffer.concat([...chunks, Buffer.alloc(1024)]));
}
function fixture(directory, target, alter = (files) => files) {
  const manifest = packageManifest(target, version, sourceRevision);
  const files = [
    ["package.json", Buffer.from(JSON.stringify(manifest))],
    ...["LICENSE", "NOTICE"].map((file) => [
      file,
      Buffer.from(sourceText(file)),
    ]),
    ["README.md", Buffer.from(sourceText("packages/binpm/README.md"))],
    ...(target
      ? [[`bin/${target.binary}`, Buffer.from("native fixture"), 0o755]]
      : ["bin/binpm.cjs", "src/launcher.cjs", "src/platforms.cjs"].map(
          (file) => [
            file,
            Buffer.from(sourceText(`packages/binpm/${file}`)),
            0o755,
          ],
        )),
  ];
  const file = path.join(directory, tarballName(manifest.name, version));
  writeFileSync(file, archive(alter(files)));
  return file;
}

test("source version is derived from Cargo and excludes reservation/prerelease versions", () => {
  assert.equal(
    metadata(() => '[package]\nname = "binpm"\nversion = "1.2.3"\n').version,
    "1.2.3",
  );
  for (const invalid of ["0.0.0", "1.2.3-next.1", "01.2.3", "../bad"])
    assert.throws(
      () =>
        metadata(() => `[package]\nname = "binpm"\nversion = "${invalid}"\n`),
      /source version/u,
    );
});

test("launcher pins all six optional native packages to the Cargo version", () => {
  const main = packageManifest(undefined, version, sourceRevision);
  assert.deepEqual(main.bin, { binpm: "bin/binpm.cjs" });
  assert.deepEqual(
    main.optionalDependencies,
    Object.fromEntries(targets.map((t) => [t.name, version])),
  );
  assert.equal(main.scripts, undefined);
  assert.equal(main.gitHead, sourceRevision);
  for (const target of targets) {
    const manifest = packageManifest(target, version, sourceRevision);
    assert.deepEqual(manifest.os, [target.os]);
    assert.deepEqual(manifest.cpu, [target.cpu]);
    assert.deepEqual(manifest.libc, target.libc ? [target.libc] : undefined);
  }
});

test("complete source-bound set is ordered native-first and rejects missing/extra files", (t) => {
  const directory = tempDirectory(t);
  for (const target of [...targets, undefined]) fixture(directory, target);
  const artifacts = verifySet(directory);
  assert.equal(artifacts.length, 7);
  assert.equal(artifacts.at(-1).name, "@delino/binpm");
  assert.throws(
    () => verifySet(directory, "a".repeat(40)),
    /metadata mismatch/u,
  );
  writeFileSync(path.join(directory, "extra.tgz"), "unexpected");
  assert.throws(() => verifySet(directory), /exactly seven/u);
  rmSync(path.join(directory, "extra.tgz"));
  rmSync(path.join(directory, artifacts[0].filename));
  assert.throws(() => verifySet(directory), /exactly seven/u);
});

test("package verification rejects payload, license, mode, path and link tampering", (t) => {
  const directory = tempDirectory(t);
  const check = (alter) =>
    inspectTarball(fixture(directory, undefined, alter), {
      version,
      sourceRevision,
    });
  assert.throws(
    () =>
      check((files) =>
        files.map(([name, bytes, ...rest]) => [
          name,
          name === "src/launcher.cjs" ? Buffer.from("changed") : bytes,
          ...rest,
        ]),
      ),
    /source mismatch/u,
  );
  assert.throws(
    () =>
      check((files) =>
        files.map(([name, bytes, ...rest]) => [
          name,
          name === "LICENSE" ? Buffer.from("changed") : bytes,
          ...rest,
        ]),
      ),
    /License mismatch/u,
  );
  assert.throws(
    () => check((files) => files.map(([name, bytes]) => [name, bytes, 0o644])),
    /executable mode/u,
  );
  assert.throws(
    () => check((files) => [...files, ["../escape", Buffer.from("bad")]]),
    /entry path/u,
  );
  assert.throws(
    () => check((files) => [...files, ["link", Buffer.from("bad"), 0o755, 50]]),
    /entry type/u,
  );
  assert.throws(
    () => check((files) => [...files, files[0]]),
    /duplicate file/u,
  );
  assert.throws(() => tarEntries(Buffer.from("invalid gzip")));
});

test("real npm pack preserves launcher sources and executable archive mode", (t) => {
  const directory = tempDirectory(t);
  const artifact = buildPackage({ output: directory });
  assert.equal(artifact.version, version);
  const entries = tarEntries(
    readFileSync(path.join(directory, "tarballs", artifact.filename)),
  );
  assert.equal(entries.get("bin/binpm.cjs").mode & 0o111, 0o111);
  assert.equal(entries.get("NOTICE").bytes.toString(), sourceText("NOTICE"));
});

const env = {
  GITHUB_REPOSITORY: "delinoio/oss",
  GITHUB_SHA: sourceRevision,
  GITHUB_EVENT_NAME: "push",
  GITHUB_REF: `refs/tags/binpm@v${version}`,
};
test("automatic and manual releases require matching source version/ref/revision", () => {
  assert.equal(releaseIdentity(env, version, sourceRevision).dry_run, "false");
  assert.equal(
    releaseIdentity(
      {
        ...env,
        GITHUB_EVENT_NAME: "workflow_dispatch",
        GITHUB_REF: "refs/heads/main",
      },
      version,
      sourceRevision,
    ).dry_run,
    "true",
  );
  assert.equal(
    releaseIdentity(
      {
        ...env,
        GITHUB_EVENT_NAME: "workflow_dispatch",
        REQUESTED_DRY_RUN: "false",
      },
      version,
      sourceRevision,
    ).dry_run,
    "false",
  );
  for (const changes of [
    { GITHUB_REF: "refs/heads/main" },
    { GITHUB_REF: "refs/tags/binpm@v9.9.9" },
    { GITHUB_SHA: "bad" },
    { GITHUB_REPOSITORY: "fork/oss" },
    { GITHUB_EVENT_NAME: "pull_request" },
  ])
    assert.throws(() =>
      releaseIdentity({ ...env, ...changes }, version, sourceRevision),
    );
  assert.throws(
    () =>
      releaseIdentity(
        {
          ...env,
          GITHUB_EVENT_NAME: "workflow_dispatch",
          REQUESTED_DRY_RUN: "false",
          GITHUB_REF: "refs/heads/main",
        },
        version,
        sourceRevision,
      ),
    /exact binpm version tag/u,
  );
});

const artifacts = [
  { name: "native", version, integrity: "x" },
  { name: "launcher", version, integrity: "y" },
];
const found = (artifact) => ({
  name: artifact.name,
  version,
  dist: { integrity: artifact.integrity },
});
test("publication preflights every integrity and confirms natives before the launcher", async () => {
  const published = new Map([["native", found(artifacts[0])]]);
  const order = [];
  await publishArtifacts(artifacts, {
    dryRun: false,
    lookup: async (a) => published.get(a.name) ?? null,
    publish: async (a) => {
      order.push(a.name);
      published.set(a.name, found(a));
    },
    delay: async () => {},
    report: () => {},
  });
  assert.deepEqual(order, ["launcher"]);
  await assert.rejects(
    publishArtifacts(artifacts, {
      dryRun: false,
      lookup: async (a) =>
        a.name === "native"
          ? null
          : { ...found(a), dist: { integrity: "conflict" } },
      publish: () => assert.fail("must preflight before uploads"),
    }),
    /Conflicting published integrity/u,
  );
});

test("readback polls once-uploaded native bytes before allowing launcher upload", async () => {
  let reads = 0;
  let uploads = 0;
  await publishArtifacts([artifacts[0]], {
    dryRun: false,
    lookup: async (a) => (++reads > 4 ? found(a) : null),
    publish: async () => {
      uploads++;
    },
    delay: async () => {},
    report: () => {},
  });
  assert.equal(uploads, 1);
  for (const lookup of [
    async () => null,
    async () => {
      if (++reads > 2) return { dist: { integrity: "bad" } };
      return null;
    },
  ]) {
    reads = 0;
    await assert.rejects(
      publishArtifacts([artifacts[0]], {
        dryRun: false,
        lookup,
        publish: async (a) => assert.equal(a.name, "native"),
        delay: async () => {},
        report: () => {},
      }),
      /integrity was not confirmed/u,
    );
  }
});

test("registry responses validate identity and integrity; dry runs make no writes", async () => {
  assert.equal(
    await registryVersion(artifacts[0], async () => ({ status: 404 })),
    null,
  );
  await assert.rejects(
    registryVersion(artifacts[0], async () => ({
      status: 200,
      ok: true,
      json: async () => ({ name: "other", version, dist: { integrity: "x" } }),
    })),
    /Invalid registry metadata/u,
  );
  await publishArtifacts(artifacts, {
    lookup: () => assert.fail("dry run must not query registry"),
    publish: () => assert.fail("dry run must not publish"),
    report: () => {},
  });
  assert.equal(trustCommands().length, 7);
  for (const command of trustCommands())
    assert.match(command, /--file release-binpm-npm.yml --allow-publish$/u);
});

test("npm publication preserves terminal output and requests provenance", () => {
  const calls = [];
  publishTarball({ name: "test", filename: "test.tgz" }, "/tarballs", {
    provenance: true,
    run: (args, options) => calls.push({ args, options }),
  });
  assert(calls[0].args.includes("--provenance"));
  assert.deepEqual(calls[0].options, { stdio: "inherit" });
});

test("target selection rejects musl/unknown hosts and distinguishes all six binaries", () => {
  for (const target of targets)
    assert.equal(
      platforms.selectTarget(target.os, target.cpu, target.libc),
      target,
    );
  assert.equal(platforms.selectTarget("linux", "x64", "musl"), undefined);
  assert.equal(platforms.selectTarget("freebsd", "x64"), undefined);
  assert.throws(
    () => launcher.resolveBinary("unused", undefined),
    /Unsupported|Missing|ENOENT/u,
  );
});

test("launcher rejects missing or mismatched native dependencies", (t) => {
  const directory = tempDirectory(t);
  const manifest = path.join(directory, "package.json");
  writeFileSync(manifest, JSON.stringify({ version }));
  const target = targets[0];
  assert.throws(
    () => launcher.resolveBinary(manifest, target),
    /optional dependencies enabled/u,
  );
  const native = path.join(directory, "node_modules", target.name);
  mkdirSync(path.join(native, "bin"), { recursive: true });
  writeFileSync(
    path.join(native, "package.json"),
    JSON.stringify({ name: target.name, version: "wrong" }),
  );
  assert.throws(
    () => launcher.resolveBinary(manifest, target),
    /Version mismatch/u,
  );
  writeFileSync(
    path.join(native, "package.json"),
    JSON.stringify({ name: target.name, version }),
  );
  writeFileSync(path.join(native, "bin", target.binary), "fixture");
  assert.equal(
    launcher.resolveBinary(manifest, target),
    realpathSync(path.join(native, "bin", target.binary)),
  );
});

test("launcher inherits stdio, forwards signals, preserves status and removes handlers", async () => {
  const parent = new EventEmitter();
  const child = new EventEmitter();
  Object.assign(child, {
    exitCode: null,
    signalCode: null,
    kill: (signal) => {
      child.forwarded = signal;
    },
  });
  const args = ["spaces here", "$(literal)", "--", 'quoted"value'];
  const pending = launcher.launch("native executable", args, {
    parent,
    start: (binary, argv, options) => {
      assert.equal(binary, "native executable");
      assert.deepEqual(argv, args);
      assert.equal(options.stdio, "inherit");
      return child;
    },
  });
  parent.emit("SIGTERM");
  assert.equal(child.forwarded, "SIGTERM");
  child.emit("exit", 37, null);
  assert.deepEqual(await pending, { code: 37, signal: null });
  assert.equal(parent.listenerCount("SIGTERM"), 0);
  const failed = launcher.launch("missing", [], { parent, start: () => child });
  child.emit("error", new Error("ENOENT"));
  await assert.rejects(failed, /Unable to start binpm/u);
  assert.equal(parent.listenerCount("SIGINT"), 0);
});
