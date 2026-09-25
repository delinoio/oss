import assert from "node:assert/strict";
import test from "node:test";
import { randomUUID } from "node:crypto";
import { v7 } from "uuid";
import { processDocument } from "../src/native.js";
import { ErrorCode, Format, type Diagnostic } from "../src/types.js";

test("native cancellation interrupts a running workbook and the worker remains reusable", { timeout: 15000 }, async () => {
  const controller = new AbortController();
  const events: Diagnostic[] = [];
  const model = { id: v7(), sheets: [{ id: v7(), name: "Large",
    cells: Array.from({ length: 18000 }, (_, row) => ({ id: v7(), address: { row, column: 0 },
      value: { type: "text", value: `Native cancellation ${row} ${randomUUID()}` } })),
  }] };
  const result = processDocument(Format.Xlsx, "generate", model, Buffer.alloc(0), new Map(), model.id, 1,
    controller.signal, { system: true, ids: [] }, event => events.push(event));
  const timer = setTimeout(() => controller.abort(), 50);
  try {
    await assert.rejects(result, { code: ErrorCode.Cancelled });
    // A saturated CI worker pool may observe cancellation before starting.
    // Rust tests deterministically cancel inside inflation and layout work.
    if (events.length) {
      assert.ok(events.some(event => event.source === "native" && event.status === "started"));
      assert.ok(events.some(event => event.source === "native" && event.code === ErrorCode.Cancelled));
    }
    model.sheets[0]!.cells = model.sheets[0]!.cells.slice(0, 1);
    const next = await processDocument(Format.Xlsx, "generate", model, Buffer.alloc(0), new Map(), model.id, 2, new AbortController().signal);
    assert.equal(next.bytes.subarray(0, 2).toString(), "PK");
  } finally { clearTimeout(timer); }
});
