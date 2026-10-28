// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { projectTaiRouterRename } from "./protocol-rename.mjs";

const buf = fileURLToPath(
  new URL("../../node_modules/@bufbuild/buf/bin/buf", import.meta.url),
);

function compare(t, mutate = () => {}) {
  const root = mkdtempSync(join(tmpdir(), "tairouter-protocol-rename-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const schema = (project, body) => `syntax = "proto3";
package ${project}.v1;
option go_package = "github.com/delinoio/oss/protos/${project}/v1;${project}v1";
${body}
`;
  const sources = {
    "common.proto": schema("aiprouter", "message Item { string value = 1; }"),
    "service.proto": schema(
      "aiprouter",
      'import "aiprouter/v1/common.proto";\nservice ItemService { rpc GetItem(Item) returns (Item); }',
    ),
    "extra.proto": schema("aiprouter", "message Extra {}"),
  };
  const build = (label, project, files) => {
    const directory = join(root, label);
    for (const [name, source] of Object.entries(files)) {
      const path = join(directory, project, "v1", name);
      mkdirSync(dirname(path), { recursive: true });
      writeFileSync(path, source);
    }
    const output = join(root, `${label}.json`);
    execFileSync(process.execPath, [
      buf,
      "build",
      directory,
      "--as-file-descriptor-set",
      "--output",
      output,
    ]);
    return JSON.parse(readFileSync(output, "utf8"));
  };
  const baseline = build("previous", "aiprouter", sources);
  const renamed = Object.fromEntries(
    Object.entries(sources).map(([name, source]) => [
      name,
      source.replaceAll("aiprouter", "tairouter"),
    ]),
  );
  mutate(renamed);
  const current = build("current", "tairouter", renamed);
  projectTaiRouterRename(baseline, current);
  const projected = join(root, "projected.json");
  writeFileSync(projected, JSON.stringify(baseline));
  return spawnSync(
    process.execPath,
    [buf, "breaking", join(root, "current.json"), "--against", projected],
    { encoding: "utf8" },
  );
}

test("namespace projection permits the exact rename with cross-file RPC types", (t) => {
  const result = compare(t);
  assert.equal(result.status, 0, result.stdout + result.stderr);
});

test("namespace projection retains field deletion checks", (t) => {
  const result = compare(t, (sources) => {
    sources["common.proto"] = sources["common.proto"].replace(
      "string value = 1;",
      "",
    );
  });
  assert.notEqual(result.status, 0);
  assert.match(result.stdout + result.stderr, /value|FIELD/u);
});

test("namespace projection retains deleted-file checks", (t) => {
  const result = compare(t, (sources) => {
    delete sources["extra.proto"];
  });
  assert.notEqual(result.status, 0);
  assert.match(result.stdout + result.stderr, /extra.proto/u);
});

test("namespace projection does not allow a different Go package", (t) => {
  const result = compare(t, (sources) => {
    sources["common.proto"] = sources["common.proto"].replace(
      "tairouterv1",
      "futurev1",
    );
  });
  assert.notEqual(result.status, 0);
});
