import { originPatternForUrl } from "./protocol.js";

const captureButton = document.querySelector("#capture");
const selectButton = document.querySelector("#select");
const sendButton = document.querySelector("#send");
const urlInput = document.querySelector("#page-url");
const titleInput = document.querySelector("#page-title");
const removeUrlButton = document.querySelector("#remove-url");
const removeTitleButton = document.querySelector("#remove-title");
const status = document.querySelector("#status");
const fields = document.querySelector("#fields");
let draft;
let sending = false;

function showStatus(message, error = false) {
  status.textContent = message;
  status.className = error ? "error" : "";
}

function sendMessage(message) {
  return chrome.runtime.sendMessage(message).then((response) => {
    if (!response?.ok) throw new Error(response?.error ?? "operation-failed");
    return response.value;
  });
}

const labels = {
  boundary: "Visual boundary",
  selector: "CSS selector",
  tag: "Tag",
  role: "Role",
  accessibleName: "Accessible name",
  viewport: "Viewport dimensions",
};

function renderSelection() {
  fields.replaceChildren();
  const selection = draft?.selection;
  if (selection === undefined) return;
  for (const [name, label] of Object.entries(labels)) {
    if (selection[name] === undefined) continue;
    const row = document.createElement("li");
    const text = document.createElement("span");
    text.textContent = label;
    const remove = document.createElement("button");
    remove.type = "button";
    remove.textContent = `Remove ${label.toLowerCase()}`;
    remove.addEventListener("click", () => {
      delete selection[name];
      renderSelection();
    });
    row.append(text, remove);
    fields.append(row);
  }
}

function displayDraft(nextDraft) {
  draft = nextDraft;
  const available = draft !== undefined;
  urlInput.value = draft?.url ?? "";
  titleInput.value = draft?.title ?? "";
  urlInput.disabled = !available;
  titleInput.disabled = !available;
  removeUrlButton.disabled = !available;
  removeTitleButton.disabled = !available;
  selectButton.disabled = !available || draft.restricted;
  sendButton.disabled = !available || sending;
  renderSelection();
}

async function restoreDraft() {
  captureButton.disabled = true;
  try {
    const restoredDraft = await sendMessage({ kind: "get-draft" });
    if (restoredDraft !== undefined) {
      displayDraft(restoredDraft);
      showStatus(
        restoredDraft.selection === undefined
          ? "Capture restored. You can optionally select a DOM boundary."
          : "Boundary selected. Remove any metadata you do not want to send.",
      );
    }
  } catch (error) {
    showStatus(error instanceof Error ? error.message : "Capture unavailable.", true);
  } finally {
    captureButton.disabled = false;
  }
}

captureButton.addEventListener("click", async () => {
  if (captureButton.disabled) return;
  captureButton.disabled = true;
  sending = false;
  displayDraft(undefined);
  showStatus("Capturing the visible viewport…");
  try {
    displayDraft(await sendMessage({ kind: "begin-capture" }));
    showStatus(
      draft.restricted
        ? "Chrome restricts this page. DevHud will use OS capture; you can edit the URL."
        : "Visible viewport captured. You can optionally select a DOM boundary.",
    );
  } catch (error) {
    showStatus(error instanceof Error ? error.message : "Capture failed.", true);
  } finally {
    captureButton.disabled = false;
  }
});

removeUrlButton.addEventListener("click", () => {
  urlInput.value = "";
});

removeTitleButton.addEventListener("click", () => {
  titleInput.value = "";
});

selectButton.addEventListener("click", async () => {
  if (draft === undefined) return;
  const origin = originPatternForUrl(draft.url);
  if (origin === null) {
    showStatus("DOM selection is unavailable on this page.", true);
    return;
  }
  try {
    const granted = await chrome.permissions.request({ origins: [origin] });
    if (!granted) {
      showStatus("Site access was denied. The viewport capture is still available.");
      return;
    }
    showStatus("Select an element on the page, or press Escape.");
    const selection = await sendMessage({
      kind: "select-boundary",
      capture: {
        captureId: draft.captureId,
        capturedTabId: draft.capturedTabId,
        capturedWindowId: draft.capturedWindowId,
        capturedUrl: draft.capturedUrl,
        origin,
      },
    });
    if (selection !== undefined) {
      draft.selection = selection;
      renderSelection();
      showStatus("Boundary selected. Remove any metadata you do not want to send.");
    } else {
      delete draft.selection;
      renderSelection();
      showStatus("DOM selection was cancelled.");
    }
  } catch (error) {
    showStatus(error instanceof Error ? error.message : "Selection failed.", true);
  }
});

sendButton.addEventListener("click", async () => {
  if (draft === undefined || sending) return;
  sending = true;
  sendButton.disabled = true;
  const submission = {
    ...draft,
    url: urlInput.value,
    title: titleInput.value,
  };
  showStatus("Opening DevHud…");
  try {
    const response = await sendMessage({
      kind: "send-to-devhud",
      draft: submission,
    });
    const accepted = response.status === "accepted";
    if (accepted) {
      displayDraft(undefined);
    } else {
      sending = false;
      sendButton.disabled = false;
    }
    showStatus(
      accepted ? "Capture sent to DevHud." : "DevHud could not accept the capture.",
      !accepted,
    );
  } catch (error) {
    sending = false;
    sendButton.disabled = false;
    showStatus(error instanceof Error ? error.message : "DevHud is unavailable.", true);
  }
});

void restoreDraft();
