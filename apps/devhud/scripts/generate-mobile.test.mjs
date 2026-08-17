import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { assertOverlayCopies } from "./generate-mobile.mjs";

test("materialized mobile projects require every expected overlay", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-mobile-overlays-"));
  const source = join(root, "source.xml");
  const destination = join(root, "generated/destination.xml");
  writeFileSync(source, "expected");
  try {
    assert.doesNotThrow(() => assertOverlayCopies([{ source, destination }], false, root));
    assert.throws(() => assertOverlayCopies([{ source, destination }], true, root), /overlay is missing/u);
    mkdirSync(join(destination, ".."), { recursive: true });
    writeFileSync(destination, "stale");
    assert.throws(() => assertOverlayCopies([{ source, destination }], true, root), /overlay is stale/u);
    writeFileSync(destination, "expected");
    assert.doesNotThrow(() => assertOverlayCopies([{ source, destination }], true, root));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
