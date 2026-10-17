export function hasCompilerDiagnostics(document) {
  if (!Array.isArray(document.diagnostics))
    throw new Error("TN_COMPILER_DIAGNOSTICS_INVALID");
  // Fail closed on every diagnostic, including warnings on a successful build.
  // Older compiler notes must never be mistaken for a clean native result.
  return (
    document.diagnostics.length !== 0 ||
    (document.notes !== undefined &&
      (!Array.isArray(document.notes) || document.notes.length !== 0))
  );
}
