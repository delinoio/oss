import { createConnectTransport } from "@connectrpc/connect-web";
import { ConnectError, Code } from "@connectrpc/connect";

export function readConnection(): { run: string } {
  const hash = new URLSearchParams(window.location.hash.slice(1));
  const run = hash.get("run") || "";
  // Discard retired pairing/port fragments without reading browser credentials.
  // Keep the non-secret run link so refresh and copied URLs retain their context.
  const fragment = new URLSearchParams();
  if (run) fragment.set("run", run);
  if (window.location.hash)
    history.replaceState(null, "", window.location.pathname + (run ? `#${fragment}` : ""));
  return { run };
}
export function transportFor() {
  return createConnectTransport({
    baseUrl: window.location.origin,
    interceptors: [
      (next) => async (request) => {
        request.header.set("X-Ach-Api-Version", "1");
        return next(request);
      },
    ],
  });
}
export function describeError(error: unknown): string {
  const e = ConnectError.from(error);
  if (e.code === Code.PermissionDenied || e.code === Code.Unauthenticated)
    return "This connection is not supported. Open the local URL printed by ach ui.";
  if (e.code === Code.Unavailable || /fetch|network/i.test(e.message))
    return "Cannot reach ach on this computer. Run ach ui to start the local server, then reload this page. In on-demand mode, keep ach ui running.";
  if (/incompatible-version/.test(e.message))
    return "The app and local ach versions are incompatible. Reload this page from the URL printed by ach ui.";
  return e.rawMessage;
}
