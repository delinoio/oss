import { HostName, createRequest, isNativeResponse, type NativeResponse } from "./protocol.js";

interface ExtensionConfiguration { readonly origins?: readonly { readonly origin: string; readonly mappingId: string }[]; readonly language?: "en" | "ko" }
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
  connectNative();
  const request = createRequest(type, payload, pairingNonce);
  if (!nativePort) return Promise.resolve({ version: 1, schema_version: 1, request_id: request.request_id, ok: false, state: "disconnected", payload: null });
  return new Promise((resolve) => {
    const timer = setTimeout(() => { pending.delete(request.request_id); resolve({ version: 1, schema_version: 1, request_id: request.request_id, ok: false, state: "disconnected", payload: null }); }, 5_000);
    pending.set(request.request_id, { resolve, timer }); nativePort!.postMessage(request);
  });
}

function injectedCapture(selectElement: boolean) {
  const allowedElements = new Set(["a", "article", "aside", "blockquote", "code", "dd", "details", "div", "dl", "dt", "em", "figcaption", "figure", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "img", "li", "main", "nav", "ol", "p", "pre", "section", "summary", "table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul"]);
  const allowedAttributes = new Set(["alt", "aria-describedby", "aria-hidden", "aria-label", "aria-labelledby", "role", "title"]);
  const encoder = new TextEncoder();
  const normalizeUrl = () => { const url = new URL(location.href); if (!/^https?:$/u.test(url.protocol)) throw new TypeError("unsupported URL"); const path = url.pathname.split("/").map((segment) => segment === "" ? "" : "<redacted>").join("/"); return `${url.protocol}//${url.hostname.toLowerCase()}${url.port ? `:${url.port}` : ""}${path}`; };
  const sanitize = (selected: Element | null) => {
    if (!selected) return "";
    const append = (parent: DocumentFragment | Element, node: Node): void => {
      if (node.nodeType === Node.TEXT_NODE) { parent.append(document.createTextNode(node.textContent ?? "")); return; }
      if (!(node instanceof Element) || !allowedElements.has(node.localName) || node.hasAttribute("hidden") || node.getAttribute("aria-hidden")?.toLowerCase() === "true") return;
      const computed = getComputedStyle(node); if (computed.display === "none" || computed.visibility === "hidden") return;
      const clean = document.createElement(node.localName);
      for (const attribute of Array.from(node.attributes)) { const name = attribute.name.toLowerCase(); if (allowedAttributes.has(name) && !(name === "aria-hidden" && attribute.value.toLowerCase() === "true")) clean.setAttribute(name, attribute.value); }
      for (const child of Array.from(node.childNodes)) append(clean, child); parent.append(clean);
    };
    const fragment = document.createDocumentFragment(); append(fragment, selected); const container = document.createElement("div"); container.append(fragment);
    while (encoder.encode(container.innerHTML).byteLength > 128 * 1024 && container.lastChild) container.lastChild.remove(); return container.innerHTML;
  };
  const result = (selected: Element | null) => { const bounds = selected?.getBoundingClientRect(); const attributes = selected ? Array.from(selected.attributes).map((attribute) => [attribute.name.toLowerCase(), attribute.value] as [string, string]) : []; const accessibility = Object.fromEntries(attributes.filter(([name, value]) => allowedAttributes.has(name) && !(name === "aria-hidden" && value.toLowerCase() === "true"))); return { url: normalizeUrl(), title: document.title, viewport: { width: innerWidth, height: innerHeight }, userAgent: navigator.userAgent, selectedBounds: bounds ? { x: bounds.x, y: bounds.y, width: bounds.width, height: bounds.height } : null, accessibility, outerHtml: sanitize(selected) }; };
  if (!selectElement) return Promise.resolve(result(null));
  return new Promise<ReturnType<typeof result> | null>((resolve) => {
    const cleanup = () => { document.removeEventListener("click", click, true); document.removeEventListener("keydown", key, true); };
    const click = (event: MouseEvent) => { event.preventDefault(); event.stopPropagation(); cleanup(); resolve(result(event.target instanceof Element ? event.target : null)); };
    const key = (event: KeyboardEvent) => { if (event.key === "Escape") { event.preventDefault(); cleanup(); resolve(null); } };
    document.addEventListener("click", click, true); document.addEventListener("keydown", key, true);
  });
}

async function capture(selectElement: boolean): Promise<NativeResponse> {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab?.id || tab.incognito || !tab.url?.match(/^https?:\/\//u)) return { version: 1, schema_version: 1, request_id: "", ok: false, state: tab?.incognito ? "denied" : "disconnected", payload: null };
  const configurationResponse = await nativeRequest("configure", {});
  if (!configurationResponse.ok) return configurationResponse;
  const configuration = (configurationResponse.payload ?? {}) as ExtensionConfiguration;
  const origin = new URL(tab.url).origin;
  const mappingId = configuration.origins?.find((configured) => configured.origin === origin)?.mappingId;
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
