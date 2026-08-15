import { terminateWindowsProcessTree } from "../../../scripts/spawn-dev-server.mjs";

function terminateTimedOutChild(child) {
  return process.platform === "win32"
    ? terminateWindowsProcessTree(child, "SIGKILL")
    : Promise.resolve(child.kill("SIGKILL"));
}

export function waitForChildClose(child, mode, timeoutMs) {
  return new Promise((resolveExit, reject) => {
    let childError;
    let timedOut = false;
    let terminationPromise;
    const timer = setTimeout(() => {
      timedOut = true;
      terminationPromise = terminateTimedOutChild(child);
      // The close handler awaits and reports termination failures after the
      // root process has also completed, so avoid an early unhandled rejection.
      void terminationPromise.catch(() => {});
    }, timeoutMs);

    child.once("error", (error) => {
      childError = error;
    });
    child.once("close", async (code) => {
      clearTimeout(timer);
      try {
        await terminationPromise;
      } catch (error) {
        reject(error);
        return;
      }
      if (timedOut) {
        reject(new Error(`${mode} smoke timed out`));
      } else if (childError) {
        reject(childError);
      } else {
        resolveExit(code);
      }
    });
  });
}
