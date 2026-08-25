import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const repositoryRoot = fileURLToPath(new URL("../..", import.meta.url));
const script = fileURLToPath(new URL("./finalize-devhud-deb.sh", import.meta.url));

test("Debian finalization makes Native Messaging registration installer-owned", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-deb-"));
  const source = join(root, "source");
  mkdirSync(join(source, "DEBIAN"), { recursive: true });
  mkdirSync(join(source, "usr/bin"), { recursive: true });
  writeFileSync(join(source, "DEBIAN/control"), "Package: devhud\nVersion: 0.1.0\nArchitecture: all\nMaintainer: DevHud\nDescription: fixture\n");
  writeFileSync(join(source, "usr/bin/devhud-native-messaging-host"), "#!/bin/sh\nexit 0\n");
  chmodSync(join(source, "usr/bin/devhud-native-messaging-host"), 0o755);
  const input = join(root, "input.deb");
  const output = join(root, "output.deb");
  execFileSync("dpkg-deb", ["--root-owner-group", "--build", source, input]);
  execFileSync("bash", [script, input, output], { cwd: repositoryRoot, env: { ...process.env, SOURCE_DATE_EPOCH: "1" } });
  const postinst = execFileSync("dpkg-deb", ["--ctrl-tarfile", output]);
  assert.ok(postinst.length > 0);
  const control = join(root, "control");
  mkdirSync(control);
  execFileSync("dpkg-deb", ["-e", output, control]);
  const install = readFileSync(join(control, "postinst"), "utf8");
  const remove = readFileSync(join(control, "prerm"), "utf8");
  assert.match(install, /\/usr\/bin\/devhud-native-messaging-host" register/u);
  assert.match(install, /\/etc\/opt\/chrome\/native-messaging-hosts/u);
  assert.match(remove, /unregister/u);
});
