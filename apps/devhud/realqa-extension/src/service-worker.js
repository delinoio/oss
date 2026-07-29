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

let activeDraft;

async function activeTab() {
  const [tab] = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
  if (tab === undefined || tab.id === undefined) throw new Error("active-tab-unavailable");
  if (tab.incognito || chrome.extension.inIncognitoContext) {
    throw new Error("incognito-excluded");
  }
  return tab;
}

async function beginCapture() {
  activeDraft = undefined;
  const tab = await activeTab();
  if (isRestrictedPage(tab.url)) {
    activeDraft = {
      captureId: crypto.randomUUID(),
      captureMode: "os-capture",
      title: tab.title,
      restricted: true,
    };
    return activeDraft;
  }
  const dataUrl = await chrome.tabs.captureVisibleTab(tab.windowId, {
    format: "png",
  });
  activeDraft = {
    captureId: crypto.randomUUID(),
    captureMode: "visible-viewport",
    capturedTabId: tab.id,
    capturedWindowId: tab.windowId,
    capturedUrl: tab.url,
    url: tab.url,
    title: tab.title,
    image: dataUrlImage(dataUrl),
    restricted: false,
  };
  return activeDraft;
}

async function selectBoundary({
  captureId,
  capturedTabId,
  capturedWindowId,
  capturedUrl,
  origin,
}) {
  if (
    activeDraft?.captureId !== captureId ||
    activeDraft.capturedTabId !== capturedTabId ||
    activeDraft.capturedWindowId !== capturedWindowId ||
    activeDraft.capturedUrl !== capturedUrl
  ) {
    throw new Error("capture-unavailable");
  }
  const tab = await activeTab();
  const pattern = originPatternForUrl(tab.url);
  if (
    tab.id !== capturedTabId ||
    tab.windowId !== capturedWindowId ||
    tab.url !== capturedUrl
  ) {
    throw new Error("captured-tab-changed");
  }
  if (pattern === null || pattern !== origin) {
    throw new Error("origin-changed");
  }
  const granted = await chrome.permissions.contains({ origins: [pattern] });
  if (!granted) throw new Error("permission-denied");
  const results = await chrome.scripting.executeScript({
    target: { tabId: tab.id, frameIds: [0] },
    func: selectDomBoundary,
  });
  const selection = sanitizeSelection(results[0]?.result);
  if (activeDraft?.captureId !== captureId) {
    throw new Error("capture-unavailable");
  }
  if (selection === undefined) {
    delete activeDraft.selection;
  } else {
    activeDraft.selection = selection;
  }
  return selection;
}

async function sendToNative(draft) {
  if (activeDraft?.captureId !== draft?.captureId) {
    throw new Error("capture-unavailable");
  }
  const captureId = activeDraft.captureId;
  const request = captureRequest({
    ...activeDraft,
    url: draft.url,
    title: draft.title,
    selection: draft.selection,
  });
  const response = await chrome.runtime.sendNativeMessage(NATIVE_HOST_NAME, request);
  assertHostResponseSize(response);
  if (response?.version !== 1 || response?.requestId !== request.requestId) {
    throw new Error("invalid-host-response");
  }
  if (response.status === "accepted" && activeDraft?.captureId === captureId) {
    activeDraft = undefined;
  }
  return response;
}

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  const operation =
    message?.kind === "begin-capture"
      ? beginCapture()
      : message?.kind === "get-draft"
        ? Promise.resolve(activeDraft)
      : message?.kind === "select-boundary"
        ? selectBoundary(message.capture)
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
