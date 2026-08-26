import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { callController, controllerRequest } from "./devhud-release-controller.mjs";

const identity = { version: "0.1.0", revision: "a".repeat(40) };

test("prepare binds immutable image references and updater bytes", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-controller-"));
  const updater = join(root, "updater.tar.gz");
  writeFileSync(updater, "signed updater material");
  const request = controllerRequest("prepare", { ...identity, updater, "api-image": "registry/devhud/api@sha256:abc", "sweeper-image": "registry/devhud/sweeper@sha256:def" });
  assert.equal(request.body.tag, "devhud@v0.1.0");
  assert.match(request.body.updater.sha256, /^[a-f0-9]{64}$/u);
  assert.equal(Buffer.from(request.body.updater.contentBase64, "base64").toString(), "signed updater material");
});

test("controller calls reject a mismatched response", async () => {
  const environment = { DEVHUD_RELEASE_CONTROLLER_URL: "https://controller.example.test", DEVHUD_RELEASE_CONTROLLER_TOKEN: "secret-token" };
  const fetchImpl = async () => new Response(JSON.stringify({ ok: true, project: "other", ...identity }), { status: 200 });
  await assert.rejects(callController("status", identity, environment, fetchImpl), /mismatched/u);
});
