import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { load } from "js-yaml";
const root = fileURLToPath(new URL("../../", import.meta.url));
const read = (path) => readFileSync(`${root}/${path}`, "utf8");
const native = load(read(".github/workflows/runlens.yml"));
const release = load(read(".github/workflows/release-runlens.yml"));
test("Runlens production documentation requires an explicit main-branch dispatch", () => {
  const docs = load(read(".github/workflows/runlens-docs.yml"));
  assert.equal(docs.on.workflow_dispatch.inputs.dry_run.default, true);
  assert.equal(docs.jobs.deploy.if, "${{ !inputs.dry_run && github.ref == 'refs/heads/main' }}");
  assert.equal(docs.jobs.deploy.environment, "runlens-docs-production");
  assert.equal(docs.jobs.deploy.needs, "build");
  assert.doesNotMatch(JSON.stringify(docs.jobs.build), /secrets\.|pages deploy/u);
});
test("Runlens uses native six-platform execution and separate minimum OS evidence", () => {
  assert.equal(native.jobs.native.strategy.matrix.include.length, 6);
  assert.equal(new Set(native.jobs.native.strategy.matrix.include.map((row) => row.target)).size, 6);
  const commands = JSON.stringify(native.jobs.native);
  for (const marker of ["cargo test --locked --features test-support", "runlens-test.py", "static.c", "--no-deps", "minimum_os", "runlens.py evidence"]) assert.ok(commands.includes(marker), marker);
  assert.deepEqual(native.permissions, { contents: "read" });
  assert.doesNotMatch(commands, /secrets\.|sign-blob|gh release/u);
});
test("Runlens dry run cannot reach publication credentials or OIDC", () => {
  assert.deepEqual(release.permissions, { contents: "read" });
  assert.equal(release.on.workflow_dispatch.inputs.dry_run.default, true);
  for (const name of ["sign", "install", "publish"]) assert.equal(release.jobs[name].if, "!inputs.dry_run", name);
  for (const name of ["build", "validate"]) assert.doesNotMatch(JSON.stringify(release.jobs[name]), /secrets\.|id-token|sign-blob/u);
  assert.deepEqual(release.jobs.sign.permissions, { contents: "read", "id-token": "write" });
  assert.deepEqual(release.jobs.publish.permissions, { contents: "write" });
  assert.deepEqual(release.jobs.publish.needs, ["validate", "sign", "install"]);
  assert.equal(release.jobs.install.strategy.matrix.include.length, 6);
  const validation = JSON.stringify(release.jobs.validate);
  for (const marker of ["run.head_sha !== context.sha", "run.conclusion !== 'success'", "context.ref", "getReleaseByTag", "readiness"]) assert.ok(validation.includes(marker), marker);
});
test("production installers require version-bound authenticated checksums and archives", () => {
  for (const file of ["runlens.sh", "runlens.ps1"]) {
    const source = read(`scripts/install/${file}`);
    for (const marker of ["verify-blob", "--certificate-identity", "https://token.actions.githubusercontent.com", "SHA256SUMS.sigstore.json", "release-runlens.yml@refs/tags/", "archive"]) assert.ok(source.toLowerCase().includes(marker.toLowerCase()), `${file}: ${marker}`);
    assert.doesNotMatch(source, /skip.verify|allow.unsigned|latest\/download/iu);
  }
});
test("Runlens lockfile and toolchain remain outside the root workspace", () => {
  assert.match(read("Cargo.toml"), /exclude = \["crates\/runlens"\]/u);
  assert.match(read("crates/runlens/rust-toolchain.toml"), /nightly-2026-08-02/u);
  assert.match(read("lefthook.yml"), /root: "crates\/runlens\/"/u);
  assert.match(read("packaging/homebrew/templates/runlens.rb.tmpl"), /bin.install "runlens"/u);
  assert.doesNotMatch(read("packaging/homebrew/templates/runlens.rb.tmpl"), /cargo build|git clone/u);
});
