// Finalize only after cleanup. Filesystem errors can contain private paths, so
// return a fixed code instead of serializing the exception or its stack.
export async function finalizeNativeResult(result, cleanup) {
  try {
    await cleanup();
    return result;
  } catch {
    return {
      ...result,
      status: "failed",
      phase: "cleanup",
      code: "TN_NATIVE_CLEANUP_FAILED",
    };
  }
}
