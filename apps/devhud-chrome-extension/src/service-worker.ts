import { injectedCapture } from "./capture.js";
import { HostName, createBoundedRequest, isNativeResponse, type NativeResponse } from "./protocol.js";
import { selectConfiguredMapping, type ExtensionConfiguration } from "./configured-mapping.js";
import { configuredOriginPermissionPattern } from "./origin-permission.js";

let nativePort: chrome.runtime.Port | null = null;
let reconnectAttempt = 0;
let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
let configurationRequestGeneration = 0;
let permissionReconciliation = Promise.resolve();
const pending = new Map<string, { resolve: (response: NativeResponse) => void; timer: ReturnType<typeof setTimeout> }>();
const configurationRevisionPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u;

interface ConfigurationSnapshot {
  readonly configuration: ExtensionConfiguration;
  readonly configurationRevision: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function isStringArray(value: unknown): value is readonly string[] {
  return Array.isArray(value) && value.every((entry) => typeof entry === "string");
}

function isAuthoritativeConfiguration(value: unknown): value is ExtensionConfiguration {
  if (!isRecord(value) || !Array.isArray(value.origins) || (value.language !== "en" && value.language !== "ko")) return false;
  return value.origins.every((configured) => isRecord(configured)
    && typeof configured.origin === "string"
    && Array.isArray(configured.mappings)
    && configured.mappings.every((mapping) => {
      if (!isRecord(mapping) || typeof mapping.mappingId !== "string" || !isRecord(mapping.matcher)) return false;
      const matcher = mapping.matcher;
      return typeof matcher.scheme === "string"
        && isStringArray(matcher.host)
        && typeof matcher.hostIsIpLiteral === "boolean"
        && typeof matcher.port === "string"
        && isStringArray(matcher.path);
    }));
}

function isConfigurationSnapshot(value: unknown): value is ConfigurationSnapshot {
  return isRecord(value)
    && isAuthoritativeConfiguration(value.configuration)
    && typeof value.configurationRevision === "string"
    && configurationRevisionPattern.test(value.configurationRevision);
}

function connectNative(): void {
  if (nativePort) return;
  try {
    const port = chrome.runtime.connectNative(HostName);
    nativePort = port;
    port.onMessage.addListener((message: unknown) => {
      if (!isNativeResponse(message)) return;
      reconnectAttempt = 0;
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

function configuredPermissionPatterns(configuration: ExtensionConfiguration): Set<string> {
  return new Set((configuration.origins ?? []).flatMap((configured) => {
    const pattern = configuredOriginPermissionPattern(configured.origin);
    return pattern ? [pattern] : [];
  }));
}

async function removeStaleOriginPermissions(configuration: ExtensionConfiguration, generation: number): Promise<void> {
  if (generation !== configurationRequestGeneration) return;
  const configured = configuredPermissionPatterns(configuration);
  const granted = await chrome.permissions.getAll();
  if (generation !== configurationRequestGeneration) return;
  const stale = (granted.origins ?? []).filter((origin) => /^https?:\/\//u.test(origin) && !configured.has(origin));
  if (stale.length > 0) await chrome.permissions.remove({ origins: stale });
}

function serializePermissionReconciliation(configuration: ExtensionConfiguration, generation: number): Promise<void> {
  const reconciliation = permissionReconciliation.then(() => removeStaleOriginPermissions(configuration, generation));
  permissionReconciliation = reconciliation.catch(() => undefined);
  return reconciliation;
}

async function configurationRequest(): Promise<{ readonly response: NativeResponse; readonly snapshot: ConfigurationSnapshot | null }> {
  const generation = ++configurationRequestGeneration;
  const response = await nativeRequest("configure", {});
  if (!response.ok) return { response, snapshot: null };
  if (!isConfigurationSnapshot(response.payload)) {
    return { response: { ...response, ok: false, state: "malformed", payload: null }, snapshot: null };
  }
  const snapshot = response.payload;
  await serializePermissionReconciliation(snapshot.configuration, generation).catch(() => undefined);
  if (generation !== configurationRequestGeneration) {
    return { response: { ...response, ok: false, state: "disconnected", payload: null }, snapshot: null };
  }
  return { response: { ...response, payload: snapshot.configuration }, snapshot };
}

async function capture(selectElement: boolean): Promise<NativeResponse> {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab?.id || tab.incognito || !tab.url?.match(/^https?:\/\//u)) return { version: 1, schema_version: 1, request_id: "", ok: false, state: tab?.incognito ? "denied" : "disconnected", payload: null };
  const initialConfiguration = await configurationRequest();
  if (!initialConfiguration.response.ok || !initialConfiguration.snapshot) return initialConfiguration.response;
  const configuration = initialConfiguration.snapshot.configuration;
  const tabOrigin = new URL(tab.url).origin;
  if (!configuration.origins?.some((candidate) => candidate.origin === tabOrigin)) return { version: 1, schema_version: 1, request_id: "", ok: false, state: "denied", payload: null };
  const permissionPattern = configuredOriginPermissionPattern(tabOrigin);
  if (!permissionPattern) return { version: 1, schema_version: 1, request_id: "", ok: false, state: "denied", payload: null };
  const permitted = await chrome.permissions.contains({ origins: [permissionPattern] }).catch(() => false);
  if (!permitted) return { version: 1, schema_version: 1, request_id: "", ok: false, state: "denied", payload: null };
  const injection = (await chrome.scripting.executeScript({ target: { tabId: tab.id }, func: injectedCapture, args: [selectElement, tabOrigin, configuration.language ?? "en"] }))[0];
  const result = injection?.result;
  if (!result) return { version: 1, schema_version: 1, request_id: "", ok: false, state: "disconnected", payload: null };
  const { liveUrl, ...context } = result;
  const currentConfigurationResponse = await configurationRequest();
  if (!currentConfigurationResponse.response.ok || !currentConfigurationResponse.snapshot) return currentConfigurationResponse.response;
  const currentConfiguration = currentConfigurationResponse.snapshot.configuration;
  const mappingId = selectConfiguredMapping(currentConfiguration, liveUrl);
  if (!mappingId) return { version: 1, schema_version: 1, request_id: "", ok: false, state: "denied", payload: null };
  return nativeRequest("capture", { configurationRevision: currentConfigurationResponse.snapshot.configurationRevision, mappingId, context });
}

chrome.runtime.onMessage.addListener((message: unknown, _sender, sendResponse) => {
  if (typeof message !== "object" || message === null) return false;
  const request = message as { type?: string; pairingNonce?: string; selectElement?: boolean };
  const work = request.type === "configuration" ? configurationRequest().then(({ response }) => response) : request.type === "pair" && typeof request.pairingNonce === "string" ? nativeRequest("pair", {}, request.pairingNonce) : request.type === "capture" ? capture(request.selectElement === true) : null;
  if (!work) return false; void work.then(sendResponse).catch(() => sendResponse({ ok: false, state: "disconnected", payload: null })); return true;
});
connectNative();
