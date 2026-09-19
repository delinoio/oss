import { createConnectTransport } from "@connectrpc/connect-web";
import { ConnectError, Code } from "@connectrpc/connect";
export interface Connection { port: number; token: string; code: string; run: string }
export function readConnection(): Connection {
  const hash = new URLSearchParams(window.location.hash.slice(1));
  const port = Number(hash.get("port") || "46309");
  const validPort = Number.isInteger(port) && port >= 1024 && port <= 65535 ? port : 46309;
  let token = "";
  try { token = localStorage.getItem(`ach-v1-browser-${validPort}`) || ""; } catch { /* A blocked store still permits a paired in-memory session. */ }
  const value = { port: validPort, token, code: hash.get("pair") || "", run: hash.get("run") || "" };
  if (window.location.hash) history.replaceState(null, "", window.location.pathname);
  return value;
}
export function transportFor(connection: Connection) {
  return createConnectTransport({ baseUrl: `http://127.0.0.1:${connection.port}`, interceptors: [(next) => async (request) => {
    request.header.set("X-Ach-Api-Version", "1");
    if (connection.token) request.header.set("Authorization", `Bearer ${connection.token}`);
    return next(request);
  }] });
}
export function describeError(error: unknown): string {
  const e = ConnectError.from(error);
  if (e.code === Code.Unauthenticated) return "Browser authorization was revoked or is missing. Run ach ui and pair this browser again.";
  if (e.code === Code.PermissionDenied) return "Access was denied. Use the official app and allow its local network access in browser settings.";
  if (e.code === Code.Unavailable || /fetch|network/i.test(e.message)) return "Cannot reach ach on this computer. Start ach daemon start (or ach ui in on-demand mode), confirm the port, and allow local network access in your browser.";
  if (/incompatible-version/.test(e.message)) return "The app and local ach versions are incompatible. Update or roll back to matching versions.";
  return e.rawMessage;
}
