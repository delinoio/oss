// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import {
  mkdtempSync,
  readFileSync,
  rmSync,
  statSync,
  unlinkSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { parseEnv } from "node:util";
import { test } from "node:test";
import { startLocal } from "./local.mjs";

function fixture(t) {
  const directory = mkdtempSync(join(tmpdir(), "aiprouter local "));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const calls = [];
  const run = async (args) => {
    calls.push(args);
    return "";
  };
  const wait = async (url) => {
    calls.push(["ready", url]);
  };
  const envfile = join(directory, ".dev-environment/aiprouter/.env");
  return { directory, calls, run, wait, envfile };
}

test("startup generates private keys once and migrates before admission", async (t) => {
  const f = fixture(t);
  await startLocal(f);
  const first = readFileSync(f.envfile, "utf8");
  const values = parseEnv(first);
  assert.equal(
    Buffer.from(JSON.parse(values.AIPROUTER_ENCRYPTION_KEYS).v1, "base64")
      .length,
    32,
  );
  assert.match(values.POSTGRES_PASSWORD, /^[a-f0-9]{48}$/);
  assert.equal(values.AIPROUTER_PUBLIC_URL, "http://127.0.0.1:8080");
  if (process.platform !== "win32")
    assert.equal(statSync(f.envfile).mode & 0o777, 0o600);
  const migration = f.calls.findIndex((args) => args.at(-1) === "migrate");
  const admission = f.calls.findIndex(
    (args) => args.slice(-3).join(" ") === "up -d aiprouter",
  );
  assert.ok(migration > f.calls.findIndex((args) => args[0] === "ready"));
  assert.ok(admission > migration);
  assert.equal(f.calls.at(-1)[1], "http://127.0.0.1:8080/readyz");
  await startLocal(f);
  assert.equal(readFileSync(f.envfile, "utf8"), first);
  await startLocal({ ...f, action: "stop" });
  assert.equal(f.calls.at(-1).at(-1), "down");
  assert.ok(!f.calls.at(-1).includes("--volumes"));
  assert.equal(readFileSync(f.envfile, "utf8"), first);
});

test("migration failure stops services and never starts API admission", async (t) => {
  const f = fixture(t);
  const run = async (args) => {
    await f.run(args);
    if (args.at(-1) === "migrate")
      throw new Error("synthetic migration failure");
    return "";
  };
  await assert.rejects(
    startLocal({ ...f, run }),
    /synthetic migration failure/,
  );
  assert.equal(f.calls.at(-1).at(-1), "stop");
  assert.ok(
    !f.calls.some((args) => args.slice(-3).join(" ") === "up -d aiprouter"),
  );
  assert.ok(readFileSync(f.envfile).length > 0);
});

test("missing keys with retained database volumes refuse startup", async (t) => {
  const f = fixture(t);
  await startLocal(f);
  unlinkSync(f.envfile);
  const run = async (args) =>
    args[0] === "volume" ? "existing-local-postgres\n" : f.run(args);
  await assert.rejects(
    startLocal({ ...f, run }),
    /encryption configuration is missing/,
  );
  assert.throws(() => readFileSync(f.envfile), { code: "ENOENT" });
});

test("Docker preflight failure leaves no generated state", async (t) => {
  const f = fixture(t);
  await assert.rejects(
    startLocal({
      ...f,
      run: async () => {
        throw new Error("Docker unavailable");
      },
    }),
    /Docker unavailable/,
  );
  assert.throws(() => statSync(join(f.directory, ".dev-environment")), {
    code: "ENOENT",
  });
});
