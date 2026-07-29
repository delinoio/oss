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

function assertCapturedTab(tab, capturedTabId, capturedWindowId, capturedUrl) {
  if (
    tab.id !== capturedTabId ||
    tab.windowId !== capturedWindowId ||
    tab.url !== capturedUrl
  ) {
    throw new Error("captured-tab-changed");
  }
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
  const capturedTab = await activeTab();
  assertCapturedTab(capturedTab, tab.id, tab.windowId, tab.url);
  activeDraft = {
    captureId: crypto.randomUUID(),
    captureMode: "visible-viewport",
    capturedTabId: capturedTab.id,
    capturedWindowId: capturedTab.windowId,
    capturedUrl: capturedTab.url,
    url: capturedTab.url,
    title: capturedTab.title,
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
  const pattern = originPatternForUrl(capturedUrl);
  if (pattern === null || pattern !== origin) {
    throw new Error("origin-changed");
  }
  let selection;
  try {
    if (
      activeDraft?.captureId !== captureId ||
      activeDraft.capturedTabId !== capturedTabId ||
      activeDraft.capturedWindowId !== capturedWindowId ||
      activeDraft.capturedUrl !== capturedUrl
    ) {
      throw new Error("capture-unavailable");
    }
    const tab = await activeTab();
    assertCapturedTab(tab, capturedTabId, capturedWindowId, capturedUrl);
    const granted = await chrome.permissions.contains({ origins: [pattern] });
    if (!granted) throw new Error("permission-denied");
    const results = await chrome.scripting.executeScript({
      target: { tabId: tab.id, frameIds: [0] },
      func: selectDomBoundary,
    });
    selection = sanitizeSelection(results[0]?.result);
  } finally {
    await chrome.permissions.remove({ origins: [pattern] });
  }
  if (activeDraft?.captureId !== captureId) {
    throw new Error("capture-unavailable");
  }
  const selectedTab = await activeTab();
  assertCapturedTab(selectedTab, capturedTabId, capturedWindowId, capturedUrl);
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
