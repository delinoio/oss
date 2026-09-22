import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

test("screenshot helper delegates clipboard copying and preserves output when unavailable", (t) => {
  const root = mkdtempSync(join(tmpdir(), "clibox clipboard-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const scripts = join(root, ".agents/skills/before-and-after/scripts");
  const bin = join(root, "bin");
  mkdirSync(join(scripts, "adapters"), { recursive: true });
  mkdirSync(bin);
  copyFileSync(new URL("../../.agents/skills/before-and-after/scripts/upload-and-copy.sh", import.meta.url), join(scripts, "upload-and-copy.sh"));
  writeFileSync(join(scripts, "adapters/0x0st.sh"), '#!/bin/sh\nprintf "https://example.invalid/%s\\n" "$(basename "$1")"\n', { mode: 0o755 });
  writeFileSync(join(bin, "node"), '#!/bin/sh\n[ "$2" = clipboard ] && [ "$3" = copy ] || exit 99\ncat > "$CLIPBOARD_FIXTURE"\nexit "${CLIPBOARD_STATUS:-0}"\n', { mode: 0o755 });
  for (const name of ["before.png", "after.png"]) writeFileSync(join(root, name), "fixture");
  const clipboard = join(root, "clipboard");
  const env = { ...process.env, PATH: `${bin}:${process.env.PATH}`, IMAGE_ADAPTER: "0x0st", CLIPBOARD_FIXTURE: clipboard };
  const args = [join(scripts, "upload-and-copy.sh"), join(root, "before.png"), join(root, "after.png")];
  const normal = execFileSync("bash", args, { env, encoding: "utf8", stdio: "pipe" });
  assert.match(normal, /URLs copied to clipboard!/u);
  assert.equal(readFileSync(clipboard, "utf8"), "Before: https://example.invalid/before.png\nAfter: https://example.invalid/after.png\n");
  const unavailable = execFileSync("bash", [...args, "--markdown"], { env: { ...env, CLIPBOARD_STATUS: "1" }, encoding: "utf8", stdio: "pipe" });
  assert.match(unavailable, /clipboard unavailable/u);
  assert.match(unavailable, /!\[Before\]\(https:\/\/example.invalid\/before.png\)/u);
  assert.match(readFileSync(clipboard, "utf8"), /^\| Before \| After \|/u);
});
