import { injectedCapture } from "./capture.js";
import { HostName, createBoundedRequest, isNativeResponse, type NativeResponse } from "./protocol.js";
import { selectConfiguredMapping, type ExtensionConfiguration } from "./configured-mapping.js";

let nativePort: chrome.runtime.Port | null = null;
let reconnectAttempt = 0;
let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
const pending = new Map<string, { resolve: (response: NativeResponse) => void; timer: ReturnType<typeof setTimeout> }>();

function connectNative(): void {
  if (nativePort) return;
  try {
    const port = chrome.runtime.connectNative(HostName);
    nativePort = port; reconnectAttempt = 0;
    port.onMessage.addListener((message: unknown) => {
      if (!isNativeResponse(message)) return;
      const request = pending.get(message.request_id); if (!request) return;
      clearTimeout(request.timer); pending.delete(message.request_id); request.resolve(message);
    });
    port.onDisconnect.addListener(() => {
      nativePort = null;
      for (const [id, request] of pending) { clearTimeout(request.timer); pending.delete(id); request.resolve({ version: 1, schema_version: 1, request_id: id, ok: false, state: "disconnected", payload: null }); }
      scheduleReconnect();
    });
  } catch { nativePort = null; scheduleReconnect(); }
}

function scheduleReconnect(): void {
  if (reconnectTimer) return;
  const delay = Math.min(30_000, 250 * (2 ** reconnectAttempt)); reconnectAttempt = Math.min(reconnectAttempt + 1, 7);
  reconnectTimer = setTimeout(() => { reconnectTimer = undefined; connectNative(); }, delay);
}

function nativeRequest(type: "pair" | "configure" | "capture" | "ping", payload: unknown, pairingNonce?: string): Promise<NativeResponse> {
  const { request, withinLimit } = createBoundedRequest(type, payload, pairingNonce);
  if (!withinLimit) return Promise.resolve({ version: 1, schema_version: 1, request_id: request.request_id, ok: false, state: "malformed", payload: null });
  connectNative();
  if (!nativePort) return Promise.resolve({ version: 1, schema_version: 1, request_id: request.request_id, ok: false, state: "disconnected", payload: null });
  return new Promise((resolve) => {
    const timer = setTimeout(() => { pending.delete(request.request_id); resolve({ version: 1, schema_version: 1, request_id: request.request_id, ok: false, state: "disconnected", payload: null }); }, 5_000);
    pending.set(request.request_id, { resolve, timer }); nativePort!.postMessage(request);
  });
}

async function capture(selectElement: boolean): Promise<NativeResponse> {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab?.id || tab.incognito || !tab.url?.match(/^https?:\/\//u)) return { version: 1, schema_version: 1, request_id: "", ok: false, state: tab?.incognito ? "denied" : "disconnected", payload: null };
  const configurationResponse = await nativeRequest("configure", {});
  if (!configurationResponse.ok) return configurationResponse;
  const configuration = (configurationResponse.payload ?? {}) as ExtensionConfiguration;
  const mappingId = selectConfiguredMapping(configuration, tab.url);
  if (!mappingId) return { version: 1, schema_version: 1, request_id: "", ok: false, state: "denied", payload: null };
  const injection = (await chrome.scripting.executeScript({ target: { tabId: tab.id }, func: injectedCapture, args: [selectElement] }))[0];
  const result = injection?.result;
  if (!result) return { version: 1, schema_version: 1, request_id: "", ok: false, state: "disconnected", payload: null };
  return nativeRequest("capture", { mappingId, context: result });
}

chrome.runtime.onMessage.addListener((message: unknown, _sender, sendResponse) => {
  if (typeof message !== "object" || message === null) return false;
  const request = message as { type?: string; pairingNonce?: string; selectElement?: boolean };
  const work = request.type === "configuration" ? nativeRequest("configure", {}) : request.type === "pair" && typeof request.pairingNonce === "string" ? nativeRequest("pair", {}, request.pairingNonce) : request.type === "capture" ? capture(request.selectElement === true) : null;
  if (!work) return false; void work.then(sendResponse).catch(() => sendResponse({ ok: false, state: "disconnected", payload: null })); return true;
});
connectNative();
