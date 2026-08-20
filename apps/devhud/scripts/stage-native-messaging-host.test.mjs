import assert from "node:assert/strict";
import test from "node:test";

import {
  cargoTargetDirectory,
  nativeMessagingHostArtifactTarget,
  nativeMessagingHostExecutable,
  rustHostTriple,
} from "./stage-native-messaging-host.mjs";

test("extracts only a bounded Rust host triple", () => {
  assert.equal(rustHostTriple("rustc 1.90.0\nhost: aarch64-apple-darwin\n"), "aarch64-apple-darwin");
  assert.throws(() => rustHostTriple("rustc 1.90.0\n"), /determine/u);
});

test("uses Cargo's emitted Native Messaging host artifact", () => {
  const executable = "/custom/cargo-target/aarch64-apple-darwin/release/devhud-native-messaging-host";
  const output = [
    "non-JSON tool output",
    JSON.stringify({
      reason: "compiler-artifact",
      target: { kind: ["lib"], name: "devhud_native_messaging_host" },
      executable: null,
    }),
    JSON.stringify({
      reason: "compiler-artifact",
      target: { kind: ["bin"], name: "devhud-native-messaging-host" },
      executable,
    }),
  ].join("\n");

  assert.equal(nativeMessagingHostExecutable(output), executable);
  assert.throws(() => nativeMessagingHostExecutable(""), /determine/u);
  assert.throws(
    () => nativeMessagingHostExecutable(`${output}\n${JSON.stringify({
      reason: "compiler-artifact",
      target: { kind: ["bin"], name: "devhud-native-messaging-host" },
      executable: `${executable}-other`,
    })}`),
    /determine/u,
  );
});

test("names the sidecar for Cargo's effective artifact target", () => {
  assert.equal(cargoTargetDirectory(JSON.stringify({ target_directory: "/custom/cargo-target" })), "/custom/cargo-target");
  assert.throws(() => cargoTargetDirectory("{}"), /target directory/u);
  assert.deepEqual(nativeMessagingHostArtifactTarget({
    executable: "/custom/cargo-target/release/devhud-native-messaging-host",
    targetDirectory: "/custom/cargo-target",
    hostTriple: "x86_64-unknown-linux-gnu",
    profile: "release",
  }), { triple: "x86_64-unknown-linux-gnu", executableSuffix: "" });
  assert.deepEqual(nativeMessagingHostArtifactTarget({
    executable: "/custom/cargo-target/aarch64-apple-darwin/release/devhud-native-messaging-host",
    targetDirectory: "/custom/cargo-target",
    hostTriple: "x86_64-apple-darwin",
    profile: "release",
  }), { triple: "aarch64-apple-darwin", executableSuffix: "" });
  assert.deepEqual(nativeMessagingHostArtifactTarget({
    executable: "/custom/cargo-target/aarch64-pc-windows-msvc/debug/devhud-native-messaging-host.exe",
    targetDirectory: "/custom/cargo-target",
    hostTriple: "x86_64-pc-windows-msvc",
    profile: "debug",
  }), { triple: "aarch64-pc-windows-msvc", executableSuffix: ".exe" });
  assert.throws(() => nativeMessagingHostArtifactTarget({
    executable: "/other/release/devhud-native-messaging-host",
    targetDirectory: "/custom/cargo-target",
    hostTriple: "x86_64-unknown-linux-gnu",
    profile: "release",
  }), /artifact target/u);
});
