// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { userInfo, tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import yaml from "js-yaml";

const root = fileURLToPath(new URL("../..", import.meta.url));
const postgresPackage = "./servers/devhud-api/internal/postgres";
const postgresCommand = `go test -count=1 -tags=integration ${postgresPackage}`;
const workflow = yaml.load(
  readFileSync(
    join(root, ".github/workflows/package-devhud-private.yml"),
    "utf8",
  ),
);
const workflowStep = workflow.jobs.oci.steps.find(
  ({ name }) => name === "Validate PostgreSQL migrations and schema readiness",
);

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: root,
    encoding: "utf8",
    timeout: 240_000,
    ...options,
  });
  if (result.error) throw result.error;
  return result;
}

function output(result) {
  return `${result.stdout ?? ""}${result.stderr ?? ""}`;
}

function expectSuccess(result, label) {
  assert.equal(result.status, 0, `${label}: ${output(result)}`);
}

async function availablePort() {
  const server = createServer();
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const { port } = server.address();
  await new Promise((resolve) => server.close(resolve));
  return port;
}

test("DevHud PostgreSQL entrypoints rerun with a warm Go result cache", async (t) => {
  for (const binary of ["initdb", "pg_ctl", "createdb", "go", "pnpm"]) {
    const result = spawnSync(
      binary,
      [binary === "go" ? "version" : "--version"],
      {
        encoding: "utf8",
      },
    );
    if (result.error || result.status !== 0) {
      t.skip(`${binary} is unavailable`);
      return;
    }
  }
  assert.equal(workflowStep.run, postgresCommand);

  const directory = mkdtempSync(join(tmpdir(), "devhud-postgres-cache-"));
  const cluster = join(directory, "cluster");
  const log = join(directory, "postgres.log");
  const port = await availablePort();
  const databaseURL = `postgres://${encodeURIComponent(userInfo().username)}@127.0.0.1:${port}/devhud_api_test?sslmode=disable`;
  const env = {
    ...process.env,
    DEVHUD_TEST_DATABASE_URL: databaseURL,
    GOCACHE: join(directory, "go-cache"),
    // Exercise the real migration/schema assertion at every command boundary
    // without repeating the package's entire database suite ten times.
    GOFLAGS: "-run=^TestSchemaCurrentUsesConfiguredSearchPath$",
  };
  const go = (args) => run("go", args, { env });
  const pgCtl = (args) => run("pg_ctl", ["-D", cluster, "-w", ...args]);
  const entrypoints = [
    {
      name: "API integration task",
      invoke: () =>
        run("pnpm", ["--filter", "devhud-api", "ci:integration"], { env }),
    },
    {
      name: "OCI integration task",
      invoke: () =>
        run("pnpm", ["--filter", "devhud-api", "ci:oci:integration"], {
          env,
        }),
    },
    {
      name: "private packaging workflow",
      invoke: () => go(workflowStep.run.split(" ").slice(1)),
    },
  ];

  try {
    expectSuccess(
      run("initdb", [
        "-D",
        cluster,
        "--auth-local=trust",
        "--auth-host=trust",
        "--no-instructions",
      ]),
      "initialize isolated PostgreSQL",
    );
    const start = () =>
      pgCtl(["-l", log, "-o", `-h 127.0.0.1 -p ${port} -F`, "start"]);
    expectSuccess(start(), "start isolated PostgreSQL");
    expectSuccess(
      run("createdb", [
        "-h",
        "127.0.0.1",
        "-p",
        String(port),
        "devhud_api_test",
      ]),
      "create isolated test database",
    );

    const seed = go(["test", "-tags=integration", postgresPackage]);
    expectSuccess(seed, "seed successful Go result");
    assert.doesNotMatch(output(seed), /\(cached\)/u);

    expectSuccess(
      pgCtl(["-m", "immediate", "stop"]),
      "stop isolated PostgreSQL",
    );
    const cached = go(["test", "-tags=integration", postgresPackage]);
    expectSuccess(cached, "confirm seeded result is reusable");
    assert.match(output(cached), /\(cached\)/u);

    for (const entrypoint of entrypoints) {
      const result = entrypoint.invoke();
      assert.notEqual(
        result.status,
        0,
        `${entrypoint.name}: ${output(result)}`,
      );
      assert.doesNotMatch(output(result), /\(cached\)/u, entrypoint.name);
    }

    expectSuccess(start(), "restart isolated PostgreSQL");
    for (const pass of [1, 2]) {
      for (const entrypoint of entrypoints) {
        const result = entrypoint.invoke();
        expectSuccess(result, `${entrypoint.name} live pass ${pass}`);
        assert.doesNotMatch(output(result), /\(cached\)/u, entrypoint.name);
      }
    }
  } finally {
    if (existsSync(cluster)) pgCtl(["-m", "immediate", "stop"]);
    rmSync(directory, { recursive: true, force: true });
  }
});
