import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

test("Runmoor validation executes subprocess tests despite a warm Go result cache", async (t) => {
  const directory = mkdtempSync(join(tmpdir(), "runmoor-go-cache-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const checkout = join(directory, "checkout");
  const command = join(checkout, "cmd");
  const runner = join(command, "internal", "runner");
  const counter = join(directory, "executions");
  mkdirSync(runner, { recursive: true });
  writeFileSync(
    join(checkout, "go.mod"),
    "module example.com/runmoorcache\n\ngo 1.22\n",
  );
  writeFileSync(
    join(runner, "runner_test.go"),
    `package runner
import (
  "fmt"
  "os"
  "os/exec"
  "path/filepath"
  "strings"
  "testing"
)
func TestCommandOutput(t *testing.T) {
  counter, err := os.OpenFile(os.Getenv("RUNMOOR_TEST_COUNTER"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
  if err != nil { t.Fatal(err) }
  if _, err := fmt.Fprintln(counter, "executed"); err != nil { t.Fatal(err) }
  if err := counter.Close(); err != nil { t.Fatal(err) }
  helper := filepath.Join(t.TempDir(), "helper")
  if output, err := exec.Command("go", "build", "-o", helper, "../..").CombinedOutput(); err != nil {
    t.Fatalf("build: %v %s", err, output)
  }
  output, err := exec.Command(helper).CombinedOutput()
  if err != nil { t.Fatal(err) }
  if actual := strings.TrimSpace(string(output)); actual != "good" {
    t.Fatalf("expected good got %s", actual)
  }
}
`,
  );
  const main = (value) =>
    `package main\nimport "fmt"\nfunc main() { fmt.Println("${value}") }\n`;
  const writeCommand = (value) =>
    writeFileSync(join(command, "main.go"), main(value));
  const env = {
    ...process.env,
    GOCACHE: join(directory, "go-cache"),
    GOWORK: "off",
    GOFLAGS: "",
    GOTOOLCHAIN: "local",
    RUNMOOR_TEST_COUNTER: counter,
  };
  const go = (flags) =>
    spawnSync("go", ["test", ...flags, "./cmd/..."], {
      cwd: checkout,
      env,
      encoding: "utf8",
      timeout: 600_000,
    });
  const executions = () =>
    readFileSync(counter, "utf8").trim().split("\n").length;

  for (const { name, flags } of [
    { name: "CI race", flags: ["-race"] },
    { name: "release plain", flags: [] },
  ]) {
    await t.test(name, () => {
      writeCommand("good");
      const seed = go(flags);
      assert.equal(seed.status, 0, seed.stdout + seed.stderr);
      const before = executions();
      const cached = go(flags);
      assert.equal(cached.status, 0, cached.stdout + cached.stderr);
      assert.match(cached.stdout, /runner\s+\(cached\)/u);
      assert.equal(executions(), before);

      // The test package does not import the command that it builds as a child.
      writeCommand("bad");
      const stale = go(flags);
      assert.equal(stale.status, 0, stale.stdout + stale.stderr);
      assert.match(stale.stdout, /runner\s+\(cached\)/u);
      assert.equal(executions(), before);

      const broken = go([...flags, "-count=1"]);
      assert.notEqual(broken.status, 0, broken.stdout + broken.stderr);
      assert.match(broken.stdout + broken.stderr, /expected good got bad/u);
      assert.doesNotMatch(broken.stdout, /\(cached\)/u);
      assert.equal(executions(), before + 1);

      writeCommand("good");
      for (let attempt = 1; attempt <= 2; attempt++) {
        const fresh = go([...flags, "-count=1"]);
        assert.equal(fresh.status, 0, fresh.stdout + fresh.stderr);
        assert.doesNotMatch(fresh.stdout, /\(cached\)/u);
        assert.equal(executions(), before + 1 + attempt);
      }
    });
  }
});
