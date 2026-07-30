/* global window */
(() => {
  const storageKey = "delidev:deck:return-to";
  const callbackPath = "/auth/devhud/callback";
  if (window.location.search || window.location.hash) {
    window.history.replaceState(null, "", callbackPath);
  }

  let returnPath = null;
  try {
    returnPath = window.sessionStorage.getItem(storageKey);
    window.sessionStorage.removeItem(storageKey);
  } catch {
    returnPath = null;
  }
  const safeReturnPath =
    returnPath === "/account" ||
    /^\/o\/[a-z0-9]+(?:-[a-z0-9]+)*\/settings$/.test(returnPath ?? "")
      ? returnPath
      : "/account";
  window.location.replace(safeReturnPath);
})();
