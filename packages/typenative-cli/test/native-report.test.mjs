import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import test from "node:test";

test("native probe failures carry unique invocation identifiers", () => {
  const probe = fileURLToPath(new URL("native-react.mjs", import.meta.url));
  const records = [0, 1].map(() => {
    const result = spawnSync(process.execPath, [probe], {
      encoding: "utf8",
      env: {
        ...process.env,
        TN_SCRIPTC: "/nonexistent/typenative-test-compiler",
      },
    });
    assert.equal(result.status, 1);
    assert.equal(result.stderr, "");
    const record = JSON.parse(result.stdout);
    assert.equal(record.status, "failed");
    assert.match(record.buildId, /^[0-9a-f-]{36}$/);
    return record;
  });
  assert.notEqual(records[0].buildId, records[1].buildId);
});
