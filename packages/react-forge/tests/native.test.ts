import assert from "node:assert/strict";
import { test } from "node:test";
import { processDocument } from "../src/native.js";
import { ForgeError } from "../src/errors.js";
import { ErrorCode, Format, Stage, type Diagnostic } from "../src/types.js";

test("native inspection failures classify scene layout separately from document import", async () => {
  for (const format of [Format.Glb, Format.Fbx, Format.Pptx, Format.Docx, Format.Xlsx, Format.Pdf, Format.Wav]) {
    const stage = format === Format.Glb || format === Format.Fbx ? Stage.Layout : Stage.Import;
    const events: Diagnostic[] = [];
    await assert.rejects(processDocument(format, "inspect", {}, Buffer.alloc(0), new Map(),
      "01959032-4d20-7000-8000-000000000001", 7, new AbortController().signal,
      { system: false, ids: [] }, event => events.push(event)),
    error => {
      assert.ok(error instanceof ForgeError);
      assert.partialDeepStrictEqual(error.context, { stage, format, revision: 7 });
      return true;
    });
    assert.deepEqual(events.map(event => [event.stage, event.status]), [[stage, "started"], [stage, "failed"]]);
    assert.ok(events.every(event => event.revision === 7 && event.format === format));
    assert.ok(Object.values(ErrorCode).includes(events[1]!.code!));
  }
});
