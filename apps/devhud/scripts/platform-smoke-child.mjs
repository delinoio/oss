export function waitForChildClose(child, mode, timeoutMs) {
  return new Promise((resolveExit, reject) => {
    let childError;
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      child.kill("SIGKILL");
    }, timeoutMs);

    child.once("error", (error) => {
      childError = error;
    });
    child.once("close", (code) => {
      clearTimeout(timer);
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
