// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { snapshotBuildInputs } from "../examples/native-todo/build-inputs.mjs";

test("todo build evidence hashes the buffers actually copied despite later source edits", async () => {
  const root = await mkdtemp(join(tmpdir(), "typenative-todo-inputs-"));
  try {
    const first = Buffer.from("uncommitted host source\n");
    await writeFile(join(root, "host.rs"), first);
    await writeFile(join(root, "app.tsx"), "<main/>\n");
    const snapshot = await snapshotBuildInputs(root, ["host.rs", "app.tsx"]);
    await writeFile(join(root, "host.rs"), "a later edit\n");
    await writeFile(join(root, "copied.rs"), snapshot.files.get("host.rs"));
    assert.deepEqual(await readFile(join(root, "copied.rs")), first);
    assert.equal(
      snapshot.hashes["host.rs"],
      createHash("sha256").update(first).digest("hex"),
    );
    assert.notEqual(
      snapshot.hashes["host.rs"],
      createHash("sha256")
        .update(await readFile(join(root, "host.rs")))
        .digest("hex"),
    );
    assert.equal(Object.keys(snapshot.hashes).length, 2);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
