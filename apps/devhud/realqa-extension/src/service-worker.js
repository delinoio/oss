import { selectDomBoundary } from "./dom-selection.js";
import {
  assertHostResponseSize,
  captureRequest,
  dataUrlImage,
  isRestrictedPage,
  NATIVE_HOST_NAME,
  originPatternForUrl,
  sanitizeSelection,
} from "./protocol.js";

async function activeTab() {
  const [tab] = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
  if (tab === undefined || tab.id === undefined) throw new Error("active-tab-unavailable");
  if (tab.incognito || chrome.extension.inIncognitoContext) {
    throw new Error("incognito-excluded");
  }
  return tab;
}

async function beginCapture() {
  const tab = await activeTab();
  if (isRestrictedPage(tab.url)) {
    return {
      captureMode: "os-capture",
      url: tab.url,
      title: tab.title,
      restricted: true,
    };
  }
  const dataUrl = await chrome.tabs.captureVisibleTab(tab.windowId, {
    format: "png",
  });
  return {
    captureMode: "visible-viewport",
    url: tab.url,
    title: tab.title,
    image: dataUrlImage(dataUrl),
    restricted: false,
  };
}

async function selectBoundary(expectedOrigin) {
  const tab = await activeTab();
  const pattern = originPatternForUrl(tab.url);
  if (pattern === null || pattern !== expectedOrigin) {
    throw new Error("origin-changed");
  }
  const granted = await chrome.permissions.contains({ origins: [pattern] });
  if (!granted) throw new Error("permission-denied");
  const results = await chrome.scripting.executeScript({
    target: { tabId: tab.id, frameIds: [0] },
    func: selectDomBoundary,
  });
  return sanitizeSelection(results[0]?.result);
}

async function sendToNative(draft) {
  const request = captureRequest(draft);
  const response = await chrome.runtime.sendNativeMessage(NATIVE_HOST_NAME, request);
  assertHostResponseSize(response);
  if (response?.version !== 1 || response?.requestId !== request.requestId) {
    throw new Error("invalid-host-response");
  }
  return response;
}

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  const operation =
    message?.kind === "begin-capture"
      ? beginCapture()
      : message?.kind === "select-boundary"
        ? selectBoundary(message.origin)
        : message?.kind === "send-to-devhud"
          ? sendToNative(message.draft)
          : Promise.reject(new Error("unsupported-message"));
  operation.then(
    (value) => sendResponse({ ok: true, value }),
    (error) =>
      sendResponse({
        ok: false,
        error: error instanceof Error ? error.message : "operation-failed",
      }),
  );
  return true;
});
