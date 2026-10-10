// SPDX-License-Identifier: Apache-2.0
import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash, generateKeyPairSync } from "node:crypto";
import { appleProvider, googleProvider } from "./providers.mjs";
import { Identity, Stage, distribute } from "./beta.mjs";
const pem = (type, options) =>
  generateKeyPairSync(type, options).privateKey.export({
    type: "pkcs8",
    format: "pem",
  });
const environment = {
  DELIDEV_MOBILE_APPLE_ISSUER: "fixture",
  DELIDEV_MOBILE_APPLE_KEY_ID: "fixture",
  DELIDEV_MOBILE_APPLE_PRIVATE_KEY: pem("ec", { namedCurve: "prime256v1" }),
  DELIDEV_MOBILE_APPLE_APP_ID: "12",
  DELIDEV_MOBILE_APPLE_INTERNAL_GROUP: "owned",
  DELIDEV_MOBILE_GOOGLE_PRINCIPAL: "fixture@fixture.iam.gserviceaccount.com",
};
environment.DELIDEV_MOBILE_GOOGLE_SERVICE_ACCOUNT = JSON.stringify({
  client_email: environment.DELIDEV_MOBILE_GOOGLE_PRINCIPAL,
  token_uri: "https://oauth2.googleapis.com/token",
  private_key: pem("rsa", { modulusLength: 2048 }),
});
const manifest = {
  candidateId: "fixture",
  identity: Identity,
  version: "0.1.0",
  iosBuild: "3",
  androidCode: "4",
  appleGroupType: "INTERNAL",
  playTrack: "internal",
  artifacts: {
    ios: { sha256: "a".repeat(64), bytes: 7 },
    android: { sha256: "b".repeat(64), bytes: 7 },
  },
};

test("Apple nested processing state and bounded group pagination preserve exact proof", async () => {
  for (const scenario of ["second-page", "absent", "processing", "missing", "external", "hash-conflict", "foreign-next", "page-bound"]) {
    let pages = 0, writes = 0;
    const fetcher = async (url, options) => {
      if (options.method !== "GET") writes++;
      if (url.endsWith("/apps/12"))
        return response({ data: { attributes: { bundleId: Identity } } });
      if (url.includes("/betaGroups/owned?"))
        return response({ data: { attributes: { isInternalGroup: true, publicLinkEnabled: false },
          relationships: { app: { data: { id: "12" } } } } });
      if (url.includes("/buildUploadFiles"))
        return response({ data: [{ attributes: { uti: "com.apple.ipa", fileSize: 7,
          sourceFileChecksums: { file: { algorithm: "SHA_256", hash: scenario === "hash-conflict" ? "wrong" : manifest.artifacts.ios.sha256 } } } }] });
      if (url.includes("/betaGroups/owned/builds")) {
        pages++;
        return response({ data: scenario === "second-page" && pages === 2 ? [{ id: "build" }] : [],
          links: { next: scenario === "foreign-next" ? "https://foreign.example/page" :
            (scenario === "page-bound" || scenario === "second-page" && pages === 1) ?
              `https://api.appstoreconnect.apple.com/v1/betaGroups/owned/builds?page=${pages + 1}` : null } });
      }
      return response({ data: { id: "upload", attributes: {
        cfBundleShortVersionString: "0.1.0", cfBundleVersion: "3", platform: "IOS",
        state: scenario === "missing" ? undefined : { state: scenario === "processing" ? "PROCESSING" : "COMPLETE" },
      } }, included: [{ type: "builds", id: "build", attributes: {
        buildAudienceType: scenario === "external" ? "APP_STORE_ELIGIBLE" : "INTERNAL_ONLY",
      } }] });
    };
    const provider = appleProvider(environment, Buffer.from("fixture"), () => {}, fetcher);
    await provider.preflight(manifest);
    if (scenario === "foreign-next") await assert.rejects(provider.inspect(manifest, { providerId: "upload" }), /pagination/);
    else {
      const result = await provider.inspect(manifest, { providerId: "upload" });
      assert.equal(result.state, ["second-page", "absent"].includes(scenario) ? "present" : "unknown", scenario);
      if (result.state === "present") assert.equal(result.distributed, scenario === "second-page");
    }
    assert.equal(writes, 0);
    if (scenario === "page-bound") assert.equal(pages, 20);
  }
});

test("Google staged membership requires commit on exact retained edit before Distributed", async () => {
  for (const scenario of ["success", "validation-failure", "lost-commit"]) {
    let committed = false, uploads = 0, commits = 0, edits = 0;
    const fetcher = async (url, options) => {
      if (url.includes("oauth2")) return response({ access_token: "fake" });
      if (url.includes("/upload")) { uploads++; assert.fail("replacement upload"); }
      if (url.endsWith("/edits") && options.method === "POST") { edits++; return response({ id: "observation" }); }
      if (committed && url.includes("/edits/original/")) return new Response("", { status: 404 });
      if (url.endsWith("/bundles")) return response({ bundles: [{ versionCode: 4, sha256: manifest.artifacts.android.sha256 }] });
      if (url.endsWith("/tracks/internal")) return response({ releases: [{ status: "completed", versionCodes: ["4"] }] });
      if (url.endsWith(":validate")) {
        assert.ok(url.includes("/edits/original:"));
        return scenario === "validation-failure" ? new Response("", { status: 400 }) : response({});
      }
      if (url.endsWith(":commit")) {
        assert.ok(url.includes("/edits/original:"));
        commits++; committed = true;
        if (scenario === "lost-commit") throw Error("lost acknowledgement with secret fixture");
        return response({});
      }
      assert.fail("unexpected operation");
    };
    const changes = [], checkpoint = (r) => changes.push(structuredClone(r));
    const provider = googleProvider(environment, Buffer.from("fixture"), checkpoint, fetcher);
    const receipt = { candidateId: manifest.candidateId, platform: "android", stage: Stage.Uploaded,
      providerId: "4", editId: "original" };
    await provider.preflight(manifest);
    assert.equal((await provider.inspect(manifest, receipt)).distributed, false);
    if (scenario === "success") {
      assert.equal((await distribute(manifest, receipt, provider, checkpoint, { dryRun: false })).stage, Stage.Distributed);
    } else {
      await assert.rejects(distribute(manifest, receipt, provider, checkpoint, { dryRun: false }));
      assert.equal(changes.some((r) => r.stage === Stage.Distributed), false);
      if (scenario === "lost-commit")
        assert.equal((await distribute(manifest, receipt, provider, checkpoint, { dryRun: false })).stage, Stage.Distributed);
    }
    assert.equal(uploads, 0);
    assert.equal(commits, scenario === "validation-failure" ? 0 : 1);
    assert.equal(edits, scenario === "validation-failure" ? 0 : 1);
  }
});
const response = (body) =>
  new Response(JSON.stringify(body), {
    headers: { "content-type": "application/json" },
  });
test("Apple app and internal group preflight rejects before upload writes", async () => {
  let writes = 0;
  const fetcher = async (url, options) => {
    if (options.method !== "GET") writes++;
    return response(
      url.includes("/apps/")
        ? { data: { attributes: { bundleId: "foreign" } } }
        : {},
    );
  };
  await assert.rejects(
    appleProvider(
      environment,
      Buffer.from("fixture"),
      () => {},
      fetcher,
    ).preflight(manifest),
  );
  assert.equal(writes, 0);
});
test("Apple unknown upload only reconciles exact original source, build audience and internal group", async () => {
  let writes = 0;
  const fetcher = async (url, options) => {
    if (options.method !== "GET") writes++;
    if (url.includes("/apps/"))
      return response({ data: { attributes: { bundleId: Identity } } });
    if (url.includes("/betaGroups/owned?") || url.endsWith("/betaGroups/owned"))
      return response({
        data: {
          attributes: { isInternalGroup: true, publicLinkEnabled: false },
          relationships: { app: { data: { id: "12" } } },
        },
      });
    if (url.includes("/buildUploadFiles"))
      return response({
        data: [
          {
            id: "file",
            attributes: {
              uti: "com.apple.ipa",
              fileSize: 7,
              sourceFileChecksums: {
                file: {
                  algorithm: "SHA_256",
                  hash: manifest.artifacts.ios.sha256,
                },
              },
            },
          },
        ],
      });
    if (url.includes("/betaGroups/owned/builds"))
      return response({ data: [{ id: "build" }] });
    return response({
      data: {
        id: "upload",
        attributes: {
          cfBundleShortVersionString: "0.1.0",
          cfBundleVersion: "3",
          platform: "IOS",
          state: { state: "COMPLETE" },
        },
      },
      included: [
        {
          type: "builds",
          id: "build",
          attributes: { buildAudienceType: "INTERNAL_ONLY" },
        },
      ],
    });
  };
  const p = appleProvider(
    environment,
    Buffer.from("fixture"),
    () => {},
    fetcher,
  );
  await p.preflight(manifest);
  const result = await p.inspect(manifest, { providerId: "upload" });
  assert.equal(result.sha256, manifest.artifacts.ios.sha256);
  assert.equal(result.distributed, true);
  assert.equal(writes, 0);
});
test("Google rejects public track and wrong protected identity before token or upload", async () => {
  let calls = 0;
  const f = () => {
    calls++;
    throw new Error("network");
  };
  await assert.rejects(
    googleProvider(environment, Buffer.from("fixture"), () => {}, f).preflight({
      ...manifest,
      playTrack: "production",
    }),
  );
  await assert.rejects(
    googleProvider(
      { ...environment, DELIDEV_MOBILE_GOOGLE_PRINCIPAL: "foreign" },
      Buffer.from("fixture"),
      () => {},
      f,
    ).preflight(manifest),
  );
  assert.equal(calls, 0);
});
test("Google inspect refuses a conflicting accepted version without sending replacement bytes", async () => {
  const calls = [];
  const f = async (url, options) => {
    calls.push([url, options]);
    if (url.includes("oauth2")) return response({ access_token: "fake" });
    if (url.endsWith("/bundles"))
      return response({
        bundles: [{ versionCode: 4, sha256: "c".repeat(64) }],
      });
    return response({ id: "original-edit" });
  };
  const p = googleProvider(environment, Buffer.from("fixture"), () => {}, f);
  await p.preflight(manifest);
  await assert.rejects(p.inspect(manifest, { editId: "original-edit" }));
  assert.equal(
    calls.some(([url]) => url.includes("/upload")),
    false,
  );
});
test("Google bundle submission uses exact hash and internal track only", async () => {
  const calls = [];
  let assigned = false;
  const f = async (url, options) => {
    calls.push([url, options]);
    if (url.includes("oauth2")) return response({ access_token: "fake" });
    if (url.includes("/upload"))
      return response({
        versionCode: 4,
        sha256: manifest.artifacts.android.sha256,
      });
    if (url.endsWith("/bundles"))
      return response({
        bundles: assigned
          ? [{ versionCode: 4, sha256: manifest.artifacts.android.sha256 }]
          : [],
      });
    if (url.endsWith("/tracks/internal")) {
      if (options.method === "PUT") {
        assigned = true;
        return response({});
      }
      return response({
        releases: assigned
          ? [{ status: "completed", versionCodes: ["4"] }]
          : [],
      });
    }
    return response({ id: "owned-edit" });
  };
  const checkpoints = [],
    p = googleProvider(
      environment,
      Buffer.from("fixture"),
      (r) => checkpoints.push(structuredClone(r)),
      f,
    ),
    r = {};
  await p.preflight(manifest);
  const result = await p.upload(manifest, r);
  assert.equal(result.sha256, manifest.artifacts.android.sha256);
  await p.assignInternal(manifest, r);
  assert.equal((await p.inspect(manifest, r)).distributed, true);
  assert.ok(checkpoints[0].editId);
  assert.equal(
    calls.some(
      ([url]) => url.includes("production") || url.includes("external"),
    ),
    false,
  );
});

test("Google lost commit acknowledgement reconciles through a new read-only edit without uploading", async () => {
  let uploads = 0,
    edits = 0;
  const checkpoints = [];
  const fetcher = async (url, options) => {
    if (url.includes("oauth2")) return response({ access_token: "fake" });
    if (url.includes("/upload")) {
      uploads++;
      throw new Error("Unexpected upload");
    }
    if (url.includes("/edits/expired/"))
      return new Response("", { status: 404 });
    if (url.endsWith("/edits") && options.method === "POST") {
      edits++;
      return response({ id: "read-edit" });
    }
    if (url.endsWith("/bundles"))
      return response({
        bundles: [
          { versionCode: 4, sha256: manifest.artifacts.android.sha256 },
        ],
      });
    if (url.endsWith("/tracks/internal"))
      return response({
        releases: [{ status: "completed", versionCodes: ["4"] }],
      });
    throw new Error("Unexpected write");
  };
  const provider = googleProvider(
    environment,
    Buffer.from("fixture"),
    (r) => checkpoints.push(structuredClone(r)),
    fetcher,
  );
  await provider.preflight(manifest);
  const receipt = { editId: "expired" };
  assert.equal((await provider.inspect(manifest, receipt)).distributed, true);
  assert.equal(uploads, 0);
  assert.equal(edits, 1);
  assert.equal(receipt.previousEditId, "expired");
  assert.ok(checkpoints.length >= 2);
});

test("Apple complete version inventory refuses reuse on a later page before creating an upload", async () => {
  let writes = 0;
  const fetcher = async (url, options) => {
    if (options.method !== "GET") writes++;
    if (url.endsWith("/apps/12"))
      return response({ data: { attributes: { bundleId: Identity } } });
    if (url.includes("/betaGroups/"))
      return response({
        data: {
          attributes: { isInternalGroup: true, publicLinkEnabled: false },
          relationships: { app: { data: { id: "12" } } },
        },
      });
    if (url.includes("page=2"))
      return response({
        data: [
          {
            attributes: {
              platform: "IOS",
              cfBundleVersion: "3",
              cfBundleShortVersionString: "different",
            },
          },
        ],
      });
    return response({
      data: [],
      links: {
        next: "https://api.appstoreconnect.apple.com/v1/apps/12/buildUploads?page=2",
      },
    });
  };
  const provider = appleProvider(
    environment,
    Buffer.from("fixture"),
    () => {},
    fetcher,
  );
  await provider.preflight(manifest);
  await assert.rejects(provider.upload(manifest, {}), /already exists/);
  assert.equal(writes, 0);
});

test("Apple returned MD5 corroborates only exact original receipt and locally verified SHA-256 bytes", async () => {
  const bytes = Buffer.from("fixture"), sha256 = createHash("sha256").update(bytes).digest("hex"),
    md5 = createHash("md5").update(bytes).digest("hex"),
    m = { ...manifest, artifacts: { ...manifest.artifacts, ios: { sha256, bytes: bytes.length } } };
  for (const scenario of ["owned", "missing-file", "foreign-file", "foreign-upload", "foreign-candidate", "wrong-md5", "wrong-sha", "processing", "external"]) {
    let writes = 0;
    const r = { candidateId: m.candidateId, providerId: "upload", fileId: "file" };
    if (scenario === "missing-file") delete r.fileId;
    if (scenario === "foreign-file") r.fileId = "foreign";
    if (scenario === "foreign-upload") r.providerId = "foreign";
    if (scenario === "foreign-candidate") r.candidateId = "foreign";
    const fetcher = async (url, options) => {
      if (options.method !== "GET") writes++;
      if (url.includes("/buildUploadFiles")) return response({ data: [{ id: "file", attributes: {
        uti: "com.apple.ipa", fileSize: bytes.length, assetDeliveryState: { state: "COMPLETE" },
        sourceFileChecksums: { file: { algorithm: "MD5", hash: scenario === "wrong-md5" ? "wrong" : md5 } },
      } }] });
      if (url.includes("/betaGroups/owned/builds")) return response({ data: [{ id: "build" }] });
      return response({ data: { id: "upload", attributes: { cfBundleShortVersionString: m.version,
        cfBundleVersion: m.iosBuild, platform: "IOS", state: { state: scenario === "processing" ? "PROCESSING" : "COMPLETE" } } },
        included: [{ type: "builds", id: "build", attributes: { buildAudienceType: scenario === "external" ? "APP_STORE_ELIGIBLE" : "INTERNAL_ONLY" } }] });
    };
    const actual = await appleProvider(environment, bytes, () => {}, fetcher).inspect(
      scenario === "wrong-sha" ? { ...m, artifacts: { ...m.artifacts, ios: { ...m.artifacts.ios, sha256: "0".repeat(64) } } } : m, r);
    assert.equal(actual.state, scenario === "owned" ? "present" : "unknown", scenario);
    if (actual.state === "present") assert.equal(actual.sha256, sha256);
    assert.equal(writes, 0);
  }
});
test("Apple explicit recovery commits only positively acknowledged original transfers", async () => {
  const bytes = Buffer.from("fixture"), sha256 = createHash("sha256").update(bytes).digest("hex"),
    m = { ...manifest, artifacts: { ...manifest.artifacts, ios: { sha256, bytes: bytes.length } } };
  for (const scenario of ["owned", "no-proof", "wrong-sha", "foreign-file", "foreign-version", "already-complete"]) {
    let commits = 0;
    const receipt = { candidateId: m.candidateId, providerId: "upload", fileId: "file", transferComplete: true,
      fileSha256: sha256, fileBytes: bytes.length };
    if (scenario === "no-proof") delete receipt.transferComplete;
    if (scenario === "wrong-sha") receipt.fileSha256 = "wrong";
    const fetcher = async (url, options) => {
      if (options.method === "PATCH") {
        commits++;
        assert.ok(url.endsWith("/buildUploadFiles/file"));
        assert.deepEqual(JSON.parse(options.body).data.attributes, { uploaded: true });
        return response({});
      }
      assert.equal(options.method, "GET");
      if (url.includes("/buildUploadFiles")) return response({ data: [{ id: scenario === "foreign-file" ? "foreign" : "file",
        attributes: { uti: "com.apple.ipa", fileSize: bytes.length, assetDeliveryState: {
          state: scenario === "already-complete" ? "COMPLETE" : "AWAITING_UPLOAD" } } }] });
      return response({ data: { id: "upload", attributes: { cfBundleShortVersionString: m.version,
        cfBundleVersion: scenario === "foreign-version" ? "9" : m.iosBuild, platform: "IOS" } } });
    };
    const provider = appleProvider(environment, bytes, () => {}, fetcher);
    if (["wrong-sha", "foreign-file", "foreign-version"].includes(scenario)) await assert.rejects(provider.recoverUpload(m, receipt));
    else await provider.recoverUpload(m, receipt);
    assert.equal(commits, scenario === "owned" ? 1 : 0, scenario);
  }
});

test("Apple upload durably acknowledges exact bytes before the checksum-free original file commit", async () => {
  const bytes = Buffer.from("fixture"), sha256 = createHash("sha256").update(bytes).digest("hex"),
    md5 = createHash("md5").update(bytes).digest("hex"),
    m = { ...manifest, artifacts: { ...manifest.artifacts, ios: { sha256, bytes: bytes.length } } },
    receipts = [], receipt = { candidateId: m.candidateId, platform: "ios", stage: Stage.Sending };
  let puts = 0, commits = 0;
  const upload = { id: "upload", attributes: { cfBundleShortVersionString: m.version,
    cfBundleVersion: m.iosBuild, platform: "IOS", state: { state: "COMPLETE" } } };
  const fetcher = async (url, options) => {
    url = String(url);
    if (url === "https://storage.example/original") {
      puts++;
      assert.equal(options.method, "PUT");
      assert.deepEqual(options.body, bytes);
      assert.equal(receipts.at(-1).fileSha256, sha256);
      assert.equal(receipts.at(-1).transferComplete, undefined);
      return new Response("", { status: 200 });
    }
    if (url.endsWith("/apps/12/buildUploads?limit=200")) return response({ data: [] });
    if (url.endsWith("/buildUploads") && options.method === "POST") return response({ data: upload });
    if (url.endsWith("/buildUploadFiles") && options.method === "POST") return response({ data: { id: "file", attributes: {
      uploadOperations: [{ method: "PUT", url: "https://storage.example/original", offset: 0, length: bytes.length, requestHeaders: [] }],
    } } });
    if (options.method === "PATCH") {
      commits++;
      assert.equal(puts, 1);
      assert.equal(receipts.at(-1).transferComplete, true);
      assert.equal(receipts.at(-1).fileId, "file");
      assert.deepEqual(JSON.parse(options.body).data.attributes, { uploaded: true });
      return response({});
    }
    if (url.includes("/buildUploadFiles")) return response({ data: [{ id: "file", attributes: {
      uti: "com.apple.ipa", fileSize: bytes.length, assetDeliveryState: { state: "COMPLETE" },
      sourceFileChecksums: { file: { algorithm: "MD5", hash: md5 } },
    } }] });
    if (url.includes("/betaGroups/owned/builds")) return response({ data: [] });
    return response({ data: upload, included: [{ type: "builds", id: "build", attributes: { buildAudienceType: "INTERNAL_ONLY" } }] });
  };
  const result = await appleProvider(environment, bytes, r => receipts.push(structuredClone(r)), fetcher).upload(m, receipt);
  assert.equal(result.state, "present");
  assert.equal(result.sha256, sha256);
  assert.equal(puts, 1);
  assert.equal(commits, 1);
});
