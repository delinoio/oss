import assert from "node:assert/strict";
import test from "node:test";
import { hasCompilerDiagnostics } from "./native-diagnostics.mjs";

test("successful compiler output must have no diagnostics or fallback notes", () => {
  assert.equal(
    hasCompilerDiagnostics({ success: true, diagnostics: [] }),
    false,
  );
  assert.equal(
    hasCompilerDiagnostics({ success: true, diagnostics: [], notes: [] }),
    false,
  );
  for (const fields of [
    { diagnostics: [{ code: "SC9999", severity: "warning" }] },
    { diagnostics: [], notes: ["fallback"] },
    { diagnostics: [], notes: "fallback" },
  ])
    assert.equal(hasCompilerDiagnostics({ success: true, ...fields }), true);
  for (const diagnostics of [undefined, null, "", {}])
    assert.throws(
      () => hasCompilerDiagnostics({ success: true, diagnostics }),
      /TN_COMPILER_DIAGNOSTICS_INVALID/,
    );
});
